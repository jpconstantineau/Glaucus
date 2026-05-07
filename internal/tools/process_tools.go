package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type TerminalTool struct{}

type ProcessTool struct {
	service *BackgroundProcessService
}

func RegisterProcessTools(registry *Registry, service *BackgroundProcessService) {
	if registry == nil {
		return
	}
	registry.Register(TerminalTool{})
	registry.Register(ProcessTool{service: service})
}

func (TerminalTool) Definition() ToolDefinition { return mustDefinition("terminal") }
func (ProcessTool) Definition() ToolDefinition  { return mustDefinition("process") }

func (TerminalTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (ProcessTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (TerminalTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()

	command, ok := getStringArg(req.Arguments, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return validationResult("command is required", started)
	}
	if blocked, reason := blockedCommandReason(command); blocked {
		return fatalResult(reason, started)
	}

	cwd, err := executionDirectory(req)
	if err != nil {
		return fatalResult(err.Error(), started)
	}
	timeoutMS := getIntArg(req.Arguments, "timeout_ms", 30000)
	if timeoutMS < 1 {
		return validationResult("timeout_ms must be at least 1", started)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	cmd := shellCommandContext(runCtx, command)
	cmd.Dir = cwd
	output, err := cmd.Output()
	stderrText := ""
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			stderrText = string(exitErr.Stderr)
		} else {
			return fatalResult(fmt.Sprintf("run command: %v", err), started)
		}
	}

	status := StatusSuccess
	display := strings.TrimSpace(string(output))
	if strings.TrimSpace(stderrText) != "" {
		if display == "" {
			display = strings.TrimSpace(stderrText)
		} else {
			display += "\n" + strings.TrimSpace(stderrText)
		}
	}
	if display == "" {
		display = "(command produced no output)"
	}
	if exitCode != 0 {
		status = StatusRecoverableError
	}

	return ToolResult{
		Status:      status,
		DisplayText: display,
		Payload: map[string]any{
			"command":   command,
			"cwd":       cwd,
			"stdout":    string(output),
			"stderr":    stderrText,
			"exit_code": exitCode,
		},
		Timing: timingSince(started),
	}
}

func (t ProcessTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.service == nil {
		return fatalResult("background process service is unavailable", started)
	}

	action, ok := getStringArg(req.Arguments, "action")
	if !ok || strings.TrimSpace(action) == "" {
		return validationResult("action is required", started)
	}

	switch action {
	case "start":
		command, ok := getStringArg(req.Arguments, "command")
		if !ok || strings.TrimSpace(command) == "" {
			return validationResult("command is required for process start", started)
		}
		if blocked, reason := blockedCommandReason(command); blocked {
			return fatalResult(reason, started)
		}
		cwd, err := executionDirectory(req)
		if err != nil {
			return fatalResult(err.Error(), started)
		}
		process, details, err := t.service.Start(ctx, StartProcessInput{
			ProfileID: req.ProfileID,
			SessionID: req.SessionID,
			RunID:     req.RunID,
			Command:   command,
			CWD:       cwd,
		})
		if err != nil {
			return fatalResult(err.Error(), started)
		}
		return ToolResult{
			Status:      StatusSuccess,
			DisplayText: fmt.Sprintf("Started background process %s.", process.Handle),
			Payload: map[string]any{
				"process": process,
				"details": details,
			},
			Timing: timingSince(started),
		}
	case "inspect":
		handle, ok := getStringArg(req.Arguments, "handle")
		if !ok || strings.TrimSpace(handle) == "" {
			return validationResult("handle is required for process inspect", started)
		}
		process, details, err := t.service.Inspect(ctx, handle)
		if err != nil {
			return fatalResult(err.Error(), started)
		}
		return ToolResult{
			Status:      StatusSuccess,
			DisplayText: fmt.Sprintf("Process %s is %s.", process.Handle, process.Status),
			Payload: map[string]any{
				"process": process,
				"details": details,
			},
			Timing: timingSince(started),
		}
	case "stop":
		handle, ok := getStringArg(req.Arguments, "handle")
		if !ok || strings.TrimSpace(handle) == "" {
			return validationResult("handle is required for process stop", started)
		}
		process, err := t.service.Stop(ctx, handle)
		if err != nil {
			return fatalResult(err.Error(), started)
		}
		return ToolResult{
			Status:      StatusSuccess,
			DisplayText: fmt.Sprintf("Stopped process %s.", process.Handle),
			Payload:     map[string]any{"process": process},
			Timing:      timingSince(started),
		}
	default:
		return validationResult("action must be one of start, inspect, or stop", started)
	}
}

func executionDirectory(req ToolRequest) (string, error) {
	cwd, _ := getStringArg(req.Arguments, "cwd")
	if strings.TrimSpace(cwd) == "" {
		if strings.TrimSpace(req.WorkingDirectory) != "" {
			return req.WorkingDirectory, nil
		}
		return req.ProfileRoot, nil
	}
	return resolveAllowedPath(req.ProfileRoot, req.WorkingDirectory, cwd)
}

func blockedCommandReason(command string) (bool, string) {
	normalized := strings.ToLower(strings.TrimSpace(command))
	blockedPatterns := []string{
		"rm -rf /",
		"remove-item -recurse",
		"shutdown",
		"reboot",
		"format ",
	}
	for _, pattern := range blockedPatterns {
		if strings.Contains(normalized, pattern) {
			return true, fmt.Sprintf("command blocked by safety rule: %s", pattern)
		}
	}
	return false, ""
}
