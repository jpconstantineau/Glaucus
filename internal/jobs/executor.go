package jobs

import (
	"context"
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
	JobID      string
	SessionID  string
	RunID      string
	OutputText string
	Status     string
}

type Executor interface {
	ExecuteJob(context.Context, Job) (ExecutionResult, error)
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

func (e RuntimeExecutor) ExecuteJob(ctx context.Context, job Job) (ExecutionResult, error) {
	if e.Sessions == nil || e.PromptBuilder == nil || e.Orchestrator == nil {
		return ExecutionResult{}, context.Canceled
	}

	requestedToolset := tools.SurfaceBackgroundJob
	if value, ok := job.ToolsetOverrides["name"].(string); ok && strings.TrimSpace(value) != "" {
		requestedToolset = strings.TrimSpace(value)
	}
	toolResolution := tools.Resolution{Surface: tools.SurfaceBackgroundJob, RequestedToolset: requestedToolset}
	if e.ToolRegistry != nil {
		toolResolution = e.ToolRegistry.Resolve(ctx, tools.ResolveRequest{
			Surface:          tools.SurfaceBackgroundJob,
			RequestedToolset: requestedToolset,
			ProfileRoot:      e.Profile.Root,
			WorkingDirectory: fallbackJobCWD(job.CWD, e.Profile.Root),
		})
	}

	session, err := e.Sessions.CreateSession(ctx, sessions.CreateSessionInput{
		ProfileID: job.ProfileID,
		Source:    "cron",
		Title:     "Cron: " + job.Name,
		Status:    "active",
		ModelSnapshot: map[string]any{
			"provider": fallbackProvider(job.ProviderOverrides, e.Config.Model.DefaultProvider),
			"model":    fallbackModel(job.ProviderOverrides, e.Config.Model.DefaultModel),
		},
		ToolsetSnapshot: map[string]any{
			"name":         requestedToolset,
			"surface":      tools.SurfaceBackgroundJob,
			"tool_names":   toolResolution.ToolNames,
			"availability": toolResolution.Availability,
		},
	})
	if err != nil {
		return ExecutionResult{}, err
	}

	userMessage, err := e.Sessions.CreateMessage(ctx, sessions.CreateMessageInput{
		ProfileID:   job.ProfileID,
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: job.Prompt}},
		VisibleText: job.Prompt,
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	sessionGoals, profileGoals, err := loadPromptGoals(ctx, e.GoalService, job.ProfileID, session.ID)
	if err != nil {
		return ExecutionResult{}, err
	}

	promptDoc, err := e.PromptBuilder.Build(agentruntime.PromptBuildInput{
		Profile:         e.Profile,
		Session:         session,
		ToolBehavior:    "This run was dispatched by the internal scheduler. Stay deterministic and finish cleanly.",
		ProjectContext:  "Scheduled job working directory: " + fallbackJobCWD(job.CWD, e.Profile.Root),
		PlatformHint:    "This turn originated from the cron scheduler surface.",
		ProviderOverlay: "Prefer the job-specific provider override when one is configured.",
		SessionGoals:    sessionGoals,
		ProfileGoals:    profileGoals,
	})
	if err != nil {
		return ExecutionResult{}, err
	}

	providerID := fallbackProvider(job.ProviderOverrides, e.Config.Model.DefaultProvider)
	modelID := fallbackModel(job.ProviderOverrides, e.Config.Model.DefaultModel)
	result, err := e.Orchestrator.Execute(ctx, agentruntime.ExecuteRunInput{
		ProfileID:      job.ProfileID,
		SessionID:      session.ID,
		TriggerSource:  "cron",
		UserMessageID:  userMessage.ID,
		Surface:        tools.SurfaceBackgroundJob,
		Actor:          "scheduler",
		ApprovalMode:   e.Config.Approvals.Mode,
		ToolResolution: toolResolution,
		Prompt:         promptDoc,
		Request: providers.NormalizedRequest{
			Messages:     []providers.RequestMessage{{Role: "user", Content: job.Prompt}},
			RequiredCaps: []string{"chat"},
		},
		Resolution: providers.ResolutionInput{
			ProviderID:           providerID,
			ModelID:              modelID,
			RequiredCapabilities: []string{"chat"},
		},
		WorkingDirectory: fallbackJobCWD(job.CWD, e.Profile.Root),
	})
	if result.Response.OutputText != "" {
		_, _ = e.Sessions.CreateMessage(context.Background(), sessions.CreateMessageInput{
			ProfileID:   job.ProfileID,
			SessionID:   session.ID,
			RunID:       result.Run.ID,
			Role:        "assistant",
			Content:     sessions.MessageContent{{Type: "output_text", Text: result.Response.OutputText}},
			VisibleText: result.Response.OutputText,
			Usage:       result.Response.Usage,
		})
	}
	return ExecutionResult{
		JobID:      job.ID,
		SessionID:  session.ID,
		RunID:      result.Run.ID,
		OutputText: result.Response.OutputText,
		Status:     result.Run.Status,
	}, err
}

func fallbackJobCWD(cwd, root string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return root
}

func fallbackProvider(overrides map[string]any, fallback string) string {
	if value, ok := overrides["provider"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func fallbackModel(overrides map[string]any, fallback string) string {
	if value, ok := overrides["model"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func loadPromptGoals(ctx context.Context, service *goals.Service, profileID, sessionID string) ([]goals.Goal, []goals.Goal, error) {
	if service == nil {
		return nil, nil, nil
	}
	return service.ListActiveGoals(ctx, profileID, sessionID)
}
