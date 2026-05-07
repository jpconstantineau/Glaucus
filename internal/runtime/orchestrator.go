package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/approvals"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

type ExecuteRunInput struct {
	ProfileID        string
	SessionID        string
	TriggerSource    string
	UserMessageID    string
	Surface          string
	Actor            string
	ApprovalMode     string
	ToolResolution   tools.Resolution
	ToolInvocation   *tools.Invocation
	Prompt           PromptDocument
	Request          providers.NormalizedRequest
	Resolution       providers.ResolutionInput
	WorkingDirectory string
}

type ExecuteRunResult struct {
	Run        sessions.Run
	Response   providers.NormalizedResponse
	Resolution providers.Resolution
	Attempts   []providers.AttemptRecord
}

type Orchestrator struct {
	sessions *sessions.Service
	router   *providers.Router
	events   *EventService
	tools    *tools.Registry
	approval *approvals.Service
}

func NewOrchestrator(sessionService *sessions.Service, router *providers.Router, eventService *EventService, registry *tools.Registry, approvalService *approvals.Service) *Orchestrator {
	return &Orchestrator{
		sessions: sessionService,
		router:   router,
		events:   eventService,
		tools:    registry,
		approval: approvalService,
	}
}

func (o *Orchestrator) Execute(ctx context.Context, input ExecuteRunInput) (ExecuteRunResult, error) {
	run, err := o.QueueRun(context.Background(), input)
	if err != nil {
		return ExecuteRunResult{}, err
	}

	return o.ProcessRun(ctx, run, input)
}

func (o *Orchestrator) QueueRun(ctx context.Context, input ExecuteRunInput) (sessions.Run, error) {
	persistCtx := context.Background()

	run, err := o.sessions.CreateRun(ctx, sessions.CreateRunInput{
		ProfileID:        input.ProfileID,
		SessionID:        input.SessionID,
		TriggerSource:    input.TriggerSource,
		Status:           RunStatusQueued,
		WorkingDirectory: input.WorkingDirectory,
		Request: map[string]any{
			"user_message_id":  input.UserMessageID,
			"surface":          input.Surface,
			"prompt":           RenderPrompt(input.Prompt),
			"prompt_fragments": input.Prompt.Fragments,
			"tool_selection":   input.ToolResolution,
			"tool_invocation":  input.ToolInvocation,
		},
	})
	if err != nil {
		return sessions.Run{}, fmt.Errorf("create run: %w", err)
	}
	o.appendEvent(persistCtx, run, "run.queued", map[string]any{"status": RunStatusQueued}, false)

	return run, nil
}

func (o *Orchestrator) ProcessRun(ctx context.Context, run sessions.Run, input ExecuteRunInput) (ExecuteRunResult, error) {
	persistCtx := context.Background()

	startedAt := time.Now().UTC()
	var err error
	run, err = o.sessions.UpdateRun(persistCtx, run.ID, sessions.UpdateRunInput{
		Status:    RunStatusRunning,
		StartedAt: startedAt,
	})
	if err != nil {
		return ExecuteRunResult{}, fmt.Errorf("mark run running: %w", err)
	}
	o.appendEvent(persistCtx, run, "run.started", map[string]any{"status": RunStatusRunning}, false)

	if input.ToolInvocation != nil {
		return o.processToolRun(ctx, run, input)
	}

	if err := ctx.Err(); err != nil {
		cancelledRun, cancelErr := o.cancelRun(context.Background(), run.ID, nil)
		if cancelErr != nil {
			return ExecuteRunResult{}, fmt.Errorf("cancel preflight run: %w", cancelErr)
		}
		return ExecuteRunResult{Run: cancelledRun}, err
	}

	response, resolution, attempts, execErr := o.router.ExecuteWithFallback(ctx, input.Resolution, input.Request)
	if execErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
			cancelledRun, cancelErr := o.cancelRun(context.Background(), run.ID, attempts)
			if cancelErr != nil {
				return ExecuteRunResult{}, fmt.Errorf("cancel run: %w", cancelErr)
			}
			return ExecuteRunResult{
				Run:        cancelledRun,
				Resolution: resolution,
				Attempts:   attempts,
			}, ctx.Err()
		}

		failedRun, updateErr := o.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
			Status:       RunStatusFailed,
			EndedAt:      time.Now().UTC(),
			ErrorCode:    "provider_error",
			ErrorMessage: execErr.Error(),
			ProviderResolution: map[string]any{
				"selected": resolution,
				"attempts": attempts,
			},
		})
		if updateErr != nil {
			return ExecuteRunResult{}, fmt.Errorf("mark run failed: %w", updateErr)
		}
		o.appendEvent(context.Background(), failedRun, "run.failed", map[string]any{
			"status":   RunStatusFailed,
			"error":    execErr.Error(),
			"attempts": attempts,
		}, true)
		return ExecuteRunResult{
			Run:        failedRun,
			Resolution: resolution,
			Attempts:   attempts,
		}, execErr
	}

	for _, chunk := range splitTextChunks(response.OutputText) {
		o.appendEvent(persistCtx, run, "assistant.delta", map[string]any{"text": chunk}, false)
	}
	o.appendEvent(persistCtx, run, "assistant.completed", map[string]any{"text": response.OutputText}, false)

	completedRun, err := o.sessions.UpdateRun(persistCtx, run.ID, sessions.UpdateRunInput{
		Status:  RunStatusCompleted,
		EndedAt: time.Now().UTC(),
		ProviderResolution: map[string]any{
			"selected": resolution,
			"attempts": attempts,
		},
	})
	if err != nil {
		return ExecuteRunResult{}, fmt.Errorf("mark run completed: %w", err)
	}
	o.appendEvent(persistCtx, completedRun, "run.completed", map[string]any{
		"status":   RunStatusCompleted,
		"attempts": attempts,
	}, true)

	return ExecuteRunResult{
		Run:        completedRun,
		Response:   response,
		Resolution: resolution,
		Attempts:   attempts,
	}, nil
}

func (o *Orchestrator) processToolRun(ctx context.Context, run sessions.Run, input ExecuteRunInput) (ExecuteRunResult, error) {
	if o.tools == nil {
		return o.failToolRun(run, "tool_runtime_unavailable", "tool runtime unavailable")
	}

	invocation := input.ToolInvocation
	if invocation == nil {
		return o.failToolRun(run, "tool_invocation_missing", "tool invocation missing")
	}

	if !containsTool(input.ToolResolution.ToolNames, invocation.Name) {
		return o.failToolRun(run, "tool_not_enabled", fmt.Sprintf("tool %q is not enabled by the selected toolset", invocation.Name))
	}

	tool, ok := o.tools.Tool(invocation.Name)
	if !ok {
		return o.failToolRun(run, "tool_not_registered", fmt.Sprintf("tool %q is not registered", invocation.Name))
	}
	definition := tool.Definition()
	if o.approval != nil {
		evaluation, err := o.approval.Evaluate(ctx, approvals.EvaluationInput{
			ProfileID:      input.ProfileID,
			SessionID:      input.SessionID,
			RunID:          run.ID,
			ToolName:       invocation.Name,
			ToolDefinition: definition,
			Arguments:      invocation.Arguments,
			Mode:           input.ApprovalMode,
			Actor:          input.Actor,
		})
		if err != nil {
			return o.failToolRun(run, "approval_evaluation_failed", err.Error())
		}
		if evaluation.Denied {
			updatedRun, updateErr := o.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
				Status:       RunStatusFailed,
				EndedAt:      time.Now().UTC(),
				ErrorCode:    "approval_denied",
				ErrorMessage: evaluation.Reason,
			})
			if updateErr != nil {
				return ExecuteRunResult{}, updateErr
			}
			o.appendEvent(context.Background(), updatedRun, "tool.approval_denied", map[string]any{
				"name":    invocation.Name,
				"reason":  evaluation.Reason,
				"request": evaluation.Request,
			}, true)
			return ExecuteRunResult{
				Run: updatedRun,
				Response: providers.NormalizedResponse{
					OutputText: evaluation.Reason,
				},
			}, errors.New(evaluation.Reason)
		}
		if evaluation.RequiresApproval {
			updatedRun, updateErr := o.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
				Status:       RunStatusFailed,
				EndedAt:      time.Now().UTC(),
				ErrorCode:    "approval_required",
				ErrorMessage: evaluation.Reason,
			})
			if updateErr != nil {
				return ExecuteRunResult{}, updateErr
			}
			o.appendEvent(context.Background(), updatedRun, "tool.approval_requested", map[string]any{
				"name":    invocation.Name,
				"reason":  evaluation.Reason,
				"request": evaluation.Request,
			}, true)
			return ExecuteRunResult{
				Run: updatedRun,
				Response: providers.NormalizedResponse{
					OutputText: "Approval required before the tool can run.",
				},
			}, errors.New("approval required")
		}
	}

	o.appendEvent(context.Background(), run, "tool.started", map[string]any{
		"name":      invocation.Name,
		"arguments": invocation.Arguments,
	}, false)

	result := tool.Execute(ctx, tools.ToolRequest{
		ProfileID:        input.ProfileID,
		SessionID:        input.SessionID,
		RunID:            run.ID,
		Surface:          input.Surface,
		ProfileRoot:      input.WorkingDirectory,
		WorkingDirectory: input.WorkingDirectory,
		Arguments:        invocation.Arguments,
	})

	status := RunStatusCompleted
	errorCode := ""
	errorMessage := ""
	eventType := "tool.completed"

	switch result.Status {
	case tools.StatusSuccess:
		o.appendEvent(context.Background(), run, "assistant.completed", map[string]any{"text": result.DisplayText}, false)
	case tools.StatusValidationError:
		status = RunStatusFailed
		errorCode = "tool_validation_error"
		errorMessage = result.DisplayText
		eventType = "tool.failed"
	case tools.StatusFatalError, tools.StatusRecoverableError:
		status = RunStatusFailed
		errorCode = "tool_execution_error"
		errorMessage = result.DisplayText
		eventType = "tool.failed"
	default:
		status = RunStatusFailed
		errorCode = "tool_execution_error"
		errorMessage = result.DisplayText
		eventType = "tool.failed"
	}

	o.appendEvent(context.Background(), run, eventType, map[string]any{
		"name":       invocation.Name,
		"status":     result.Status,
		"payload":    result.Payload,
		"diagnostic": result.Diagnostics,
		"text":       result.DisplayText,
	}, true)

	updatedRun, err := o.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
		Status:  status,
		EndedAt: time.Now().UTC(),
		ProviderResolution: map[string]any{
			"tool_result": map[string]any{
				"name":   invocation.Name,
				"status": result.Status,
				"result": result.Payload,
			},
		},
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
	})
	if err != nil {
		return ExecuteRunResult{}, fmt.Errorf("update tool run: %w", err)
	}

	if status != RunStatusCompleted {
		return ExecuteRunResult{
			Run: updatedRun,
			Response: providers.NormalizedResponse{
				OutputText: result.DisplayText,
			},
		}, errors.New(result.DisplayText)
	}

	return ExecuteRunResult{
		Run: updatedRun,
		Response: providers.NormalizedResponse{
			OutputText: result.DisplayText,
		},
	}, nil
}

func (o *Orchestrator) failToolRun(run sessions.Run, code, message string) (ExecuteRunResult, error) {
	updatedRun, err := o.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
		Status:       RunStatusFailed,
		EndedAt:      time.Now().UTC(),
		ErrorCode:    code,
		ErrorMessage: message,
	})
	if err != nil {
		return ExecuteRunResult{}, err
	}
	o.appendEvent(context.Background(), updatedRun, "tool.failed", map[string]any{
		"status": RunStatusFailed,
		"error":  message,
	}, true)
	return ExecuteRunResult{Run: updatedRun}, errors.New(message)
}

func (o *Orchestrator) cancelRun(ctx context.Context, runID string, attempts []providers.AttemptRecord) (sessions.Run, error) {
	run, err := o.sessions.UpdateRun(ctx, runID, sessions.UpdateRunInput{
		Status:       RunStatusCancelled,
		EndedAt:      time.Now().UTC(),
		ErrorCode:    "cancelled",
		ErrorMessage: "run cancelled",
		ProviderResolution: map[string]any{
			"attempts": attempts,
			"cancellation": map[string]any{
				"cancelled_at": time.Now().UTC().Format(time.RFC3339Nano),
			},
		},
	})
	if err != nil {
		return sessions.Run{}, err
	}
	o.appendEvent(ctx, run, "run.cancelled", map[string]any{
		"status":   RunStatusCancelled,
		"attempts": attempts,
	}, true)
	return run, nil
}

func (o *Orchestrator) appendEvent(ctx context.Context, run sessions.Run, eventType string, payload map[string]any, terminal bool) {
	if o.events == nil {
		return
	}
	_, _ = o.events.Append(ctx, AppendEventInput{
		ProfileID:  run.ProfileID,
		RunID:      run.ID,
		SessionID:  run.SessionID,
		Type:       eventType,
		Payload:    payload,
		IsTerminal: terminal,
	})
}

func splitTextChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	words := strings.Fields(text)
	if len(words) <= 1 {
		return []string{text}
	}

	chunks := make([]string, 0, len(words))
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > 24 {
			chunks = append(chunks, current+" ")
			current = word
			continue
		}
		current += " " + word
	}
	chunks = append(chunks, current)
	return chunks
}

func containsTool(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
