package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

type CronJobCreateInput struct {
	ProfileID         string
	Name              string
	Prompt            string
	ScheduleKind      string
	ScheduleValue     string
	Timezone          string
	Enabled           bool
	DeliveryTarget    map[string]any
	ToolsetOverrides  map[string]any
	ProviderOverrides map[string]any
	CWD               string
}

type CronJobUpdateInput struct {
	Name              string
	Prompt            string
	ScheduleKind      string
	ScheduleValue     string
	Timezone          string
	Enabled           *bool
	DeliveryTarget    map[string]any
	ToolsetOverrides  map[string]any
	ProviderOverrides map[string]any
	CWD               string
}

type CronJobManager interface {
	ListJobs(context.Context, string, int) (any, error)
	GetJob(context.Context, string) (any, error)
	CreateJob(context.Context, CronJobCreateInput) (any, error)
	UpdateJob(context.Context, string, CronJobUpdateInput) (any, error)
	PauseJob(context.Context, string) (any, error)
	ResumeJob(context.Context, string) (any, error)
	DeleteJob(context.Context, string) error
	QueueManualRun(context.Context, string, string) (any, any, error)
	ListJobRuns(context.Context, string, int) (any, error)
}

func RegisterJobTools(registry *Registry, manager CronJobManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(CronJobTool{manager: manager})
}

type CronJobTool struct {
	manager CronJobManager
}

func (t CronJobTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "cronjob",
		Description: "Create, manage, and inspect durable scheduled jobs.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string"},
				"job_id":   map[string]any{"type": "string"},
				"name":     map[string]any{"type": "string"},
				"prompt":   map[string]any{"type": "string"},
				"schedule": map[string]any{"type": "object"},
				"timezone": map[string]any{"type": "string"},
				"cwd":      map[string]any{"type": "string"},
				"delivery": map[string]any{"type": "object"},
				"toolset":  map[string]any{"type": "object"},
				"provider": map[string]any{"type": "object"},
				"limit":    map[string]any{"type": "integer"},
			},
			"required": []string{"action"},
		},
		Toolsets:     []string{"cronjob"},
		Flags:        ToolFlags{ApprovalSensitive: true},
		Concurrency:  "single-flight",
		DisplayGroup: "process",
	}
}

func (t CronJobTool) CheckAvailability(ctx context.Context, req AvailabilityRequest) AvailabilityResult {
	_ = ctx
	_ = req
	if t.manager == nil {
		return AvailabilityResult{Available: false, Reason: "jobs service is unavailable"}
	}
	return AvailabilityResult{Available: true}
}

func (t CronJobTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	if t.manager == nil {
		return ToolResult{Status: StatusFatalError, DisplayText: "jobs service is unavailable"}
	}

	action := strings.ToLower(strings.TrimSpace(stringArg(req.Arguments, "action")))
	if action == "" {
		return ToolResult{Status: StatusValidationError, DisplayText: "action is required"}
	}

	switch action {
	case "list":
		items, err := t.manager.ListJobs(ctx, req.ProfileID, intArg(req.Arguments, "limit", 50))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"jobs": items}, DisplayText: fmt.Sprintf("Listed %d cron jobs.", sliceLen(items))}
	case "create":
		scheduleKind, scheduleValue := scheduleArgs(req.Arguments)
		job, err := t.manager.CreateJob(ctx, CronJobCreateInput{
			ProfileID:         req.ProfileID,
			Name:              stringArg(req.Arguments, "name"),
			Prompt:            stringArg(req.Arguments, "prompt"),
			ScheduleKind:      scheduleKind,
			ScheduleValue:     scheduleValue,
			Timezone:          fallbackString(stringArg(req.Arguments, "timezone"), "UTC"),
			Enabled:           boolArg(req.Arguments, "enabled", true),
			DeliveryTarget:    mapArg(req.Arguments, "delivery"),
			ToolsetOverrides:  mapArg(req.Arguments, "toolset"),
			ProviderOverrides: mapArg(req.Arguments, "provider"),
			CWD:               fallbackString(stringArg(req.Arguments, "cwd"), req.WorkingDirectory),
		})
		if err != nil {
			return ToolResult{Status: StatusValidationError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job}, DisplayText: "Created cron job."}
	case "view":
		job, err := t.manager.GetJob(ctx, stringArg(req.Arguments, "job_id"))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job}, DisplayText: "Loaded cron job."}
	case "update":
		scheduleKind, scheduleValue := scheduleArgs(req.Arguments)
		job, err := t.manager.UpdateJob(ctx, stringArg(req.Arguments, "job_id"), CronJobUpdateInput{
			Name:              stringArg(req.Arguments, "name"),
			Prompt:            stringArg(req.Arguments, "prompt"),
			ScheduleKind:      scheduleKind,
			ScheduleValue:     scheduleValue,
			Timezone:          stringArg(req.Arguments, "timezone"),
			Enabled:           optionalBool(req.Arguments, "enabled"),
			DeliveryTarget:    mapArg(req.Arguments, "delivery"),
			ToolsetOverrides:  mapArg(req.Arguments, "toolset"),
			ProviderOverrides: mapArg(req.Arguments, "provider"),
			CWD:               stringArg(req.Arguments, "cwd"),
		})
		if err != nil {
			return ToolResult{Status: StatusValidationError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job}, DisplayText: "Updated cron job."}
	case "pause":
		job, err := t.manager.PauseJob(ctx, stringArg(req.Arguments, "job_id"))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job}, DisplayText: "Paused cron job."}
	case "resume":
		job, err := t.manager.ResumeJob(ctx, stringArg(req.Arguments, "job_id"))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job}, DisplayText: "Resumed cron job."}
	case "delete":
		if err := t.manager.DeleteJob(ctx, stringArg(req.Arguments, "job_id")); err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, DisplayText: "Deleted cron job."}
	case "run_now":
		job, jobRun, err := t.manager.QueueManualRun(ctx, req.ProfileID, stringArg(req.Arguments, "job_id"))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job": job, "job_run": jobRun}, DisplayText: "Queued a manual run."}
	case "history":
		items, err := t.manager.ListJobRuns(ctx, stringArg(req.Arguments, "job_id"), intArg(req.Arguments, "limit", 20))
		if err != nil {
			return ToolResult{Status: StatusFatalError, DisplayText: err.Error()}
		}
		return ToolResult{Status: StatusSuccess, Payload: map[string]any{"job_runs": items}, DisplayText: fmt.Sprintf("Loaded %d cron job runs.", sliceLen(items))}
	default:
		return ToolResult{Status: StatusValidationError, DisplayText: fmt.Sprintf("unsupported action %q", action)}
	}
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func mapArg(args map[string]any, key string) map[string]any {
	if args == nil {
		return nil
	}
	value, _ := args[key].(map[string]any)
	return value
}

func intArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	switch value := args[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return fallback
	}
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	value, ok := args[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func optionalBool(args map[string]any, key string) *bool {
	if args == nil {
		return nil
	}
	value, ok := args[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func scheduleArgs(args map[string]any) (string, string) {
	schedule := mapArg(args, "schedule")
	if schedule == nil {
		return "", ""
	}
	return stringArg(schedule, "kind"), stringArg(schedule, "value")
}

func fallbackString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func sliceLen(value any) int {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return rv.Len()
	}
	return 0
}
