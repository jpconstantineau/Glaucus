package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ToolResolution   tools.Resolution
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
}

func NewOrchestrator(sessionService *sessions.Service, router *providers.Router, eventService *EventService) *Orchestrator {
	return &Orchestrator{
		sessions: sessionService,
		router:   router,
		events:   eventService,
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
