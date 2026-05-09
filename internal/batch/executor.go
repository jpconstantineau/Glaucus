package batch

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/goals"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	agentruntime "github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type ExecutionResult struct {
	JobID          string
	CompletedCount int
	FailedCount    int
	AttemptedCount int
	Status         string
	ExportPath     string
	LastSessionID  string
	LastRunID      string
	LastOutputText string
	LastError      string
}

type RuntimeExecutor struct {
	Profile       profile.ActiveProfile
	Config        config.Config
	Sessions      *sessions.Service
	GoalService   *goals.Service
	PromptBuilder *agentruntime.PromptBuilder
	Orchestrator  *agentruntime.Orchestrator
	ToolRegistry  *tools.Registry
}

func (e RuntimeExecutor) ExecuteJob(ctx context.Context, service *Service, job Job) (ExecutionResult, error) {
	if service == nil || e.Sessions == nil || e.PromptBuilder == nil || e.Orchestrator == nil {
		return ExecutionResult{}, errors.New("batch runtime unavailable")
	}

	if job.StartedAt.IsZero() {
		updated, err := service.UpdateJob(ctx, job.ID, UpdateJobInput{
			Status:         JobStatusRunning,
			CompletedCount: job.CompletedCount,
			FailedCount:    job.FailedCount,
			StartedAt:      service.now(),
		})
		if err != nil {
			return ExecutionResult{}, err
		}
		job = updated
	}

	attempts, err := service.ListRunnableAttempts(ctx, job.ID)
	if err != nil {
		return ExecutionResult{}, err
	}
	result := ExecutionResult{JobID: job.ID}
	var lastErr error

	for _, attempt := range attempts {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		attempt, err = service.UpdateAttempt(ctx, attempt.ID, UpdateAttemptInput{
			Status:    AttemptStatusRunning,
			StartedAt: service.now(),
		})
		if err != nil {
			return result, err
		}

		runResult, execErr := e.executeAttempt(ctx, job, attempt)
		result.AttemptedCount++
		result.LastSessionID = runResult.SessionID
		result.LastRunID = runResult.RunID
		result.LastOutputText = runResult.OutputText

		updateInput := UpdateAttemptInput{
			SessionID:  runResult.SessionID,
			RunID:      runResult.RunID,
			OutputText: runResult.OutputText,
			Usage:      runResult.Usage,
			EndedAt:    service.now(),
		}
		if execErr != nil || runResult.Status != agentruntime.RunStatusCompleted {
			updateInput.Status = AttemptStatusFailed
			updateInput.ErrorMessage = firstNonEmpty(errorString(execErr), runResult.ErrorMessage, "batch attempt failed")
			result.LastError = updateInput.ErrorMessage
			lastErr = execErr
		} else {
			updateInput.Status = AttemptStatusCompleted
		}

		if _, err := service.UpdateAttempt(ctx, attempt.ID, updateInput); err != nil {
			return result, err
		}
	}

	updatedJob, err := service.RecomputeJob(ctx, job.ID)
	if err != nil {
		return result, err
	}

	remaining, err := service.ListRunnableAttempts(ctx, job.ID)
	if err != nil {
		return result, err
	}
	if len(remaining) == 0 {
		updatedJob, err = service.UpdateJob(ctx, updatedJob.ID, UpdateJobInput{
			Status:         updatedJob.Status,
			CompletedCount: updatedJob.CompletedCount,
			FailedCount:    updatedJob.FailedCount,
			StartedAt:      updatedJob.StartedAt,
			EndedAt:        service.now(),
		})
		if err != nil {
			return result, err
		}
	}

	exportBundle, exportErr := service.WriteTrajectoryExport(ctx, updatedJob.ID, e.Profile.Root)
	if exportErr == nil {
		result.ExportPath = updatedJob.ExportPath
		if result.ExportPath == "" {
			result.ExportPath = exportBundle.TrajectoryPath
		}
	}

	result.CompletedCount = updatedJob.CompletedCount
	result.FailedCount = updatedJob.FailedCount
	result.Status = updatedJob.Status
	if exportErr != nil && lastErr == nil {
		lastErr = exportErr
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, lastErr
}

type attemptExecutionResult struct {
	SessionID    string
	RunID        string
	OutputText   string
	Usage        map[string]any
	Status       string
	ErrorMessage string
}

func (e RuntimeExecutor) executeAttempt(ctx context.Context, job Job, attempt Attempt) (attemptExecutionResult, error) {
	requestedToolset := firstNonEmpty(job.Toolset, tools.SurfaceBackgroundJob)
	toolResolution := tools.Resolution{Surface: tools.SurfaceBackgroundJob, RequestedToolset: requestedToolset}
	if e.ToolRegistry != nil {
		toolResolution = e.ToolRegistry.Resolve(ctx, tools.ResolveRequest{
			Surface:          tools.SurfaceBackgroundJob,
			RequestedToolset: requestedToolset,
			ProfileRoot:      e.Profile.Root,
			WorkingDirectory: fallbackWorkingDirectory(job.WorkingDirectory, e.Profile.Root),
		})
	}

	session, err := e.Sessions.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID: job.ProfileID,
		Source:    "batch",
		Title:     "Batch: " + job.Name + " #" + itoa(attempt.ItemIndex),
		Status:    "active",
		ModelSnapshot: map[string]any{
			"provider": job.ProviderID,
			"model":    job.ModelID,
		},
		ToolsetSnapshot: map[string]any{
			"name":         requestedToolset,
			"surface":      tools.SurfaceBackgroundJob,
			"tool_names":   toolResolution.ToolNames,
			"availability": toolResolution.Availability,
		},
		Metadata: map[string]any{
			"batch_job_id":     job.ID,
			"batch_attempt_id": attempt.ID,
			"batch_item_id":    attempt.ItemID,
			"batch_item_index": attempt.ItemIndex,
		},
	})
	if err != nil {
		return attemptExecutionResult{}, err
	}

	userMessage, err := e.Sessions.CreateMessage(ctx, sessions.CreateMessageInput{
		ProfileID:   job.ProfileID,
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: attempt.Prompt}},
		VisibleText: attempt.Prompt,
	})
	if err != nil {
		return attemptExecutionResult{}, err
	}

	sessionGoals, profileGoals, err := loadPromptGoals(ctx, e.GoalService, job.ProfileID, session.ID)
	if err != nil {
		return attemptExecutionResult{}, err
	}
	promptDoc, err := e.PromptBuilder.Build(agentruntime.PromptBuildInput{
		Profile:         e.Profile,
		Session:         session,
		ToolBehavior:    "This run was dispatched by the batch processor. Preserve machine-readable output integrity.",
		ProjectContext:  "Batch working directory: " + fallbackWorkingDirectory(job.WorkingDirectory, e.Profile.Root),
		PlatformHint:    "This turn originated from the batch processing surface.",
		ProviderOverlay: "Prefer the batch job provider and model selection for consistent replay.",
		SessionGoals:    sessionGoals,
		ProfileGoals:    profileGoals,
	})
	if err != nil {
		return attemptExecutionResult{}, err
	}

	runResult, execErr := e.Orchestrator.Execute(ctx, agentruntime.ExecuteRunInput{
		ProfileID:      job.ProfileID,
		SessionID:      session.ID,
		TriggerSource:  "batch",
		UserMessageID:  userMessage.ID,
		Surface:        tools.SurfaceBackgroundJob,
		Actor:          "batch",
		ApprovalMode:   e.Config.Approvals.Mode,
		ToolResolution: toolResolution,
		Prompt:         promptDoc,
		Request: providers.NormalizedRequest{
			Messages:     []providers.RequestMessage{{Role: "user", Content: attempt.Prompt}},
			RequiredCaps: []string{"chat"},
		},
		Resolution: providers.ResolutionInput{
			ProviderID:           job.ProviderID,
			ModelID:              job.ModelID,
			RequiredCapabilities: []string{"chat"},
		},
		WorkingDirectory: fallbackWorkingDirectory(job.WorkingDirectory, e.Profile.Root),
	})
	if runResult.Response.OutputText != "" {
		_, _ = e.Sessions.CreateMessage(context.Background(), sessions.CreateMessageInput{
			ProfileID:   job.ProfileID,
			SessionID:   session.ID,
			RunID:       runResult.Run.ID,
			Role:        "assistant",
			Content:     sessions.MessageContent{{Type: "output_text", Text: runResult.Response.OutputText}},
			VisibleText: runResult.Response.OutputText,
			Usage:       runResult.Response.Usage,
		})
	}

	return attemptExecutionResult{
		SessionID:    session.ID,
		RunID:        runResult.Run.ID,
		OutputText:   runResult.Response.OutputText,
		Usage:        runResult.Response.Usage,
		Status:       runResult.Run.Status,
		ErrorMessage: runResult.Run.ErrorMessage,
	}, execErr
}

func fallbackWorkingDirectory(cwd, root string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return root
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func loadPromptGoals(ctx context.Context, service *goals.Service, profileID, sessionID string) ([]goals.Goal, []goals.Goal, error) {
	if service == nil {
		return nil, nil, nil
	}
	return service.ListActiveGoals(ctx, profileID, sessionID)
}
