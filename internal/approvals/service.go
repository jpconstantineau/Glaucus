package approvals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const CollectionApprovalRequests = "approval_requests"

const (
	ModeManual      = "manual"
	ModeSmart       = "smart"
	ModeOff         = "off"
	ModeYoloRun     = "yolo_run"
	ModeYoloSession = "yolo_session"
)

type Request struct {
	ID        string
	ProfileID string
	RunID     string
	ToolName  string
	Request   map[string]any
	Decision  string
	DecidedBy string
	DecidedAt time.Time
	Scope     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type EvaluationInput struct {
	ProfileID      string
	SessionID      string
	RunID          string
	ToolName       string
	ToolDefinition tools.ToolDefinition
	Arguments      map[string]any
	Mode           string
	Actor          string
}

type EvaluationResult struct {
	Allowed          bool
	RequiresApproval bool
	Denied           bool
	Reason           string
	Request          *Request
}

type Service struct {
	app    core.App
	config config.ApprovalsConfig
}

func NewService(app core.App, cfg config.ApprovalsConfig) *Service {
	return &Service{app: app, config: cfg}
}

func (s *Service) Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error) {
	mode := normalizeMode(input.Mode, s.config.Mode)
	fingerprint, summary, err := fingerprintFor(input.ToolName, input.Arguments)
	if err != nil {
		return EvaluationResult{}, err
	}

	if blocked, reason := s.blockedReason(input.ToolName, input.Arguments); blocked {
		req, reqErr := s.createRequest(ctx, input, fingerprint, summary, "deny", "blocked")
		if reqErr != nil {
			return EvaluationResult{}, reqErr
		}
		return EvaluationResult{Denied: true, Reason: reason, Request: &req}, nil
	}

	decision, err := s.findMatchingDecision(ctx, input.ProfileID, input.ToolName, input.SessionID, fingerprint)
	if err != nil {
		return EvaluationResult{}, err
	}
	if decision != nil {
		switch decision.Scope {
		case "once", "session", "permanent":
			return EvaluationResult{Allowed: true, Reason: "allowed by prior approval", Request: decision}, nil
		case "blocked":
			return EvaluationResult{Denied: true, Reason: "denied by prior approval", Request: decision}, nil
		}
	}

	if mode == ModeOff || mode == ModeYoloRun || mode == ModeYoloSession {
		return EvaluationResult{Allowed: true}, nil
	}

	if !input.ToolDefinition.Flags.ApprovalSensitive && mode == ModeSmart {
		return EvaluationResult{Allowed: true}, nil
	}

	request, err := s.createRequest(ctx, input, fingerprint, summary, "pending", "once")
	if err != nil {
		return EvaluationResult{}, err
	}
	return EvaluationResult{
		RequiresApproval: true,
		Reason:           "approval required",
		Request:          &request,
	}, nil
}

func (s *Service) Decide(ctx context.Context, id, decision, scope, actor string) (Request, error) {
	record, err := s.app.FindRecordById(CollectionApprovalRequests, id)
	if err != nil {
		return Request{}, fmt.Errorf("find approval request: %w", err)
	}
	record.Set("decision", decision)
	record.Set("scope", scope)
	record.Set("decided_by", actor)
	dt, err := types.ParseDateTime(time.Now().UTC())
	if err == nil {
		record.Set("decided_at", dt)
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Request{}, fmt.Errorf("save approval decision: %w", err)
	}
	return requestFromRecord(record)
}

func (s *Service) ListPending(ctx context.Context, profileID string) ([]Request, error) {
	records, err := s.app.FindRecordsByFilter(
		CollectionApprovalRequests,
		"profile_id = {:profile_id} && decision = 'pending'",
		"created",
		0,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}

	requests := make([]Request, 0, len(records))
	for _, record := range records {
		request, err := requestFromRecord(record)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	_ = ctx
	return requests, nil
}

func (s *Service) ListRecent(ctx context.Context, profileID string, limit int) ([]Request, error) {
	records, err := s.app.FindRecordsByFilter(CollectionApprovalRequests, "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return nil, fmt.Errorf("list approval history: %w", err)
	}

	requests := make([]Request, 0, len(records))
	for _, record := range records {
		request, err := requestFromRecord(record)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	sort.SliceStable(requests, func(i, j int) bool {
		return requests[i].CreatedAt.After(requests[j].CreatedAt)
	})
	if limit > 0 && len(requests) > limit {
		requests = requests[:limit]
	}
	_ = ctx
	return requests, nil
}

func (s *Service) findMatchingDecision(ctx context.Context, profileID, toolName, sessionID, fingerprint string) (*Request, error) {
	records, err := s.app.FindRecordsByFilter(
		CollectionApprovalRequests,
		"profile_id = {:profile_id} && tool_name = {:tool_name} && decision != 'pending'",
		"",
		0,
		0,
		dbx.Params{"profile_id": profileID, "tool_name": toolName},
	)
	if err != nil {
		return nil, fmt.Errorf("list prior approvals: %w", err)
	}

	for _, record := range records {
		request, err := requestFromRecord(record)
		if err != nil {
			return nil, err
		}
		if requestFingerprint(request.Request) != fingerprint {
			continue
		}
		if request.Scope == "session" && requestSessionID(request.Request) != sessionID {
			continue
		}
		return &request, nil
	}

	_ = ctx
	return nil, nil
}

func (s *Service) createRequest(ctx context.Context, input EvaluationInput, fingerprint, summary, decision, scope string) (Request, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollectionApprovalRequests)
	if err != nil {
		return Request{}, fmt.Errorf("find approval request collection: %w", err)
	}
	record := core.NewRecord(collection)
	record.Set("profile_id", input.ProfileID)
	record.Set("run_id", input.RunID)
	record.Set("tool_name", input.ToolName)
	record.Set("decision", decision)
	record.Set("scope", scope)

	request := map[string]any{
		"session_id":   input.SessionID,
		"actor":        input.Actor,
		"mode":         normalizeMode(input.Mode, s.config.Mode),
		"summary":      summary,
		"arguments":    input.Arguments,
		"fingerprint":  fingerprint,
		"requested_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Request{}, fmt.Errorf("marshal approval request: %w", err)
	}
	record.Set("request_json", string(raw))
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Request{}, fmt.Errorf("save approval request: %w", err)
	}
	return requestFromRecord(record)
}

func (s *Service) blockedReason(toolName string, arguments map[string]any) (bool, string) {
	if toolName != "terminal" && toolName != "process" {
		return false, ""
	}
	command, _ := arguments["command"].(string)
	normalized := strings.ToLower(strings.TrimSpace(command))
	blockedPatterns := []string{
		"rm -rf /",
		"remove-item -recurse",
		"shutdown",
		"reboot",
		"format ",
	}
	blockedPatterns = append(blockedPatterns, s.config.BlockPatterns...)
	for _, pattern := range blockedPatterns {
		if strings.TrimSpace(pattern) != "" && strings.Contains(normalized, strings.ToLower(pattern)) {
			return true, fmt.Sprintf("command blocked by approval policy: %s", pattern)
		}
	}
	return false, ""
}

func normalizeMode(mode string, fallback string) string {
	normalized := strings.TrimSpace(mode)
	if normalized == "" {
		normalized = strings.TrimSpace(fallback)
	}
	if normalized == "" {
		return ModeManual
	}
	return normalized
}

func fingerprintFor(toolName string, arguments map[string]any) (string, string, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "", "", fmt.Errorf("marshal approval fingerprint: %w", err)
	}
	sum := sha256.Sum256(append([]byte(toolName+":"), raw...))
	summary := toolName + " " + string(raw)
	return hex.EncodeToString(sum[:]), summary, nil
}

func requestFingerprint(request map[string]any) string {
	value, _ := request["fingerprint"].(string)
	return value
}

func requestSessionID(request map[string]any) string {
	value, _ := request["session_id"].(string)
	return value
}

func requestFromRecord(record *core.Record) (Request, error) {
	request := Request{
		ID:        record.Id,
		ProfileID: record.GetString("profile_id"),
		RunID:     record.GetString("run_id"),
		ToolName:  record.GetString("tool_name"),
		Decision:  record.GetString("decision"),
		DecidedBy: record.GetString("decided_by"),
		DecidedAt: record.GetDateTime("decided_at").Time(),
		Scope:     record.GetString("scope"),
		CreatedAt: record.GetDateTime("created").Time(),
		UpdatedAt: record.GetDateTime("updated").Time(),
	}
	raw := record.GetString("request_json")
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &request.Request); err != nil {
			return Request{}, fmt.Errorf("decode approval request: %w", err)
		}
	}
	return request, nil
}
