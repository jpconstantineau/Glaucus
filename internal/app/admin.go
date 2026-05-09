package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/goals"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	agentruntime "github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/jpconstantineau/Glaucus/internal/web"
)

type DoctorReport struct {
	ProfileSlug     string   `json:"profile_slug"`
	ProfileRoot     string   `json:"profile_root"`
	ConfigPath      string   `json:"config_path"`
	ProvidersDir    string   `json:"providers_dir"`
	ProviderCount   int      `json:"provider_count"`
	DefaultProvider string   `json:"default_provider"`
	DefaultModel    string   `json:"default_model"`
	WebBindAddress  string   `json:"web_bind_address"`
	APIBearerTokens int      `json:"api_bearer_tokens"`
	PocketBaseDir   string   `json:"pocketbase_dir"`
	ApprovalsMode   string   `json:"approvals_mode"`
	Warnings        []string `json:"warnings"`
}

func (r *Runtime) PrepareStorage() error {
	if err := r.pocketbase.Bootstrap(); err != nil {
		return err
	}
	if err := r.pocketbase.RunAllMigrations(); err != nil {
		return err
	}
	return web.EnsureDefaultOperator(r.pocketbase, r.server.operatorEmail, r.server.operatorPassword)
}

func (r *Runtime) CloseStorage() error {
	return r.pocketbase.ResetBootstrapState()
}

func (r *Runtime) Doctor(_ context.Context) (DoctorReport, error) {
	report := DoctorReport{
		ProfileSlug:     r.profile.Slug,
		ProfileRoot:     r.profile.Root,
		ConfigPath:      r.config.ConfigPath,
		ProvidersDir:    filepath.Join("providers", "manifests"),
		ProviderCount:   len(r.providers.Entries),
		DefaultProvider: r.config.Config.Model.DefaultProvider,
		DefaultModel:    r.config.Config.Model.DefaultModel,
		WebBindAddress:  r.config.Config.Web.BindAddress,
		APIBearerTokens: len(r.config.Config.API.BearerTokens),
		PocketBaseDir:   r.config.Config.PocketBase.DataDir,
		ApprovalsMode:   r.config.Config.Approvals.Mode,
	}
	for _, entry := range r.providers.Entries {
		if entry.ProviderID == report.DefaultProvider && entry.ModelID == report.DefaultModel {
			return report, nil
		}
	}
	report.Warnings = append(report.Warnings, "default provider/model is not present in the loaded catalog")
	return report, nil
}

func (r *Runtime) CreateProfileExport(ctx context.Context, createdBy string) (exports.Record, error) {
	if err := r.PrepareStorage(); err != nil {
		return exports.Record{}, err
	}
	defer r.CloseStorage()

	return r.exports.CreateProfileExport(ctx, exports.ExportInput{
		ProfileID:   r.profile.Slug,
		ProfileRoot: r.profile.Root,
		CreatedBy:   createdBy,
	})
}

func (r *Runtime) ValidateImportPackage(path string) (exports.ValidationResult, error) {
	return r.exports.ValidateImportPackage(path)
}

func (r *Runtime) ExecutePrompt(ctx context.Context, text string) (string, error) {
	if err := r.PrepareStorage(); err != nil {
		return "", err
	}
	defer r.CloseStorage()

	_, output, err := r.executePromptRun(ctx, text, "cli.prompt", "cli", tools.SurfaceAPIDefault)
	return output, err
}

func (r *Runtime) ExecutePromptRun(ctx context.Context, text string) (sessions.Run, string, error) {
	if err := r.PrepareStorage(); err != nil {
		return sessions.Run{}, "", err
	}
	defer r.CloseStorage()

	return r.executePromptRun(ctx, text, "acp.prompt", "acp", tools.SurfaceAPIDefault)
}

func (r *Runtime) GetRun(ctx context.Context, runID string) (sessions.Run, error) {
	if err := r.PrepareStorage(); err != nil {
		return sessions.Run{}, err
	}
	defer r.CloseStorage()

	return r.sessions.GetRun(ctx, runID)
}

func (r *Runtime) ListRunEvents(ctx context.Context, runID string, after int) ([]agentruntime.RunEvent, error) {
	if err := r.PrepareStorage(); err != nil {
		return nil, err
	}
	defer r.CloseStorage()

	return r.events.ListRunEvents(ctx, runID, after)
}

func (r *Runtime) executePromptRun(ctx context.Context, text, source, actor, surface string) (sessions.Run, string, error) {

	session, err := r.sessions.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID: r.profile.Slug,
		Source:    source,
		Title:     summarizePromptTitle(text),
		Status:    "active",
		ModelSnapshot: map[string]any{
			"provider": r.config.Config.Model.DefaultProvider,
			"model":    r.config.Config.Model.DefaultModel,
		},
	})
	if err != nil {
		return sessions.Run{}, "", err
	}

	userMessage, err := r.sessions.CreateMessage(ctx, sessions.CreateMessageInput{
		ProfileID:   r.profile.Slug,
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: text}},
		VisibleText: text,
	})
	if err != nil {
		return sessions.Run{}, "", err
	}

	toolResolution := tools.Resolution{Surface: surface, RequestedToolset: tools.SurfaceAPIDefault}
	if r.tools != nil {
		toolResolution = r.tools.Resolve(ctx, tools.ResolveRequest{
			Surface:          surface,
			RequestedToolset: tools.SurfaceAPIDefault,
			ProfileRoot:      r.profile.Root,
			WorkingDirectory: r.profile.Root,
		})
	}

	promptDoc, err := r.prompts.Build(agentruntime.PromptBuildInput{
		Profile:         r.profile,
		Session:         session,
		ToolBehavior:    "This prompt was launched from the CLI. Keep the answer concise and machine-friendly.",
		ProjectContext:  "CLI one-shot prompt execution.",
		PlatformHint:    "This turn originated from the Glaucus CLI surface.",
		ProviderOverlay: "Use the configured default CLI provider and model.",
		SessionGoals:    r.mustListSessionGoals(ctx, session.ID),
		ProfileGoals:    r.mustListProfileGoals(ctx),
	})
	if err != nil {
		return sessions.Run{}, "", err
	}

	result, err := r.runs.Execute(ctx, agentruntime.ExecuteRunInput{
		ProfileID:      r.profile.Slug,
		SessionID:      session.ID,
		TriggerSource:  source,
		UserMessageID:  userMessage.ID,
		Surface:        surface,
		Actor:          actor,
		ApprovalMode:   r.config.Config.Approvals.Mode,
		ToolResolution: toolResolution,
		Prompt:         promptDoc,
		Request: providers.NormalizedRequest{
			Messages:     []providers.RequestMessage{{Role: "user", Content: text}},
			RequiredCaps: []string{"chat"},
		},
		Resolution: providers.ResolutionInput{
			ProviderID:           r.config.Config.Model.DefaultProvider,
			ModelID:              r.config.Config.Model.DefaultModel,
			RequiredCapabilities: []string{"chat"},
		},
		WorkingDirectory: r.profile.Root,
	})
	if result.Response.OutputText != "" {
		_, _ = r.sessions.CreateMessage(context.Background(), sessions.CreateMessageInput{
			ProfileID:   r.profile.Slug,
			SessionID:   session.ID,
			RunID:       result.Run.ID,
			Role:        "assistant",
			Content:     sessions.MessageContent{{Type: "output_text", Text: result.Response.OutputText}},
			VisibleText: result.Response.OutputText,
			Usage:       result.Response.Usage,
		})
	}
	return result.Run, strings.TrimSpace(result.Response.OutputText), err
}

func summarizePromptTitle(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= 48 {
		return text
	}
	return strings.TrimSpace(text[:48]) + "..."
}

func (r *Runtime) mustListSessionGoals(ctx context.Context, sessionID string) []goals.Goal {
	if r.goals == nil {
		return nil
	}
	sessionGoals, _, err := r.goals.ListActiveGoals(ctx, r.profile.Slug, sessionID)
	if err != nil {
		return nil
	}
	return sessionGoals
}

func (r *Runtime) mustListProfileGoals(ctx context.Context) []goals.Goal {
	if r.goals == nil {
		return nil
	}
	_, profileGoals, err := r.goals.ListActiveGoals(ctx, r.profile.Slug, "")
	if err != nil {
		return nil
	}
	return profileGoals
}
