package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jpconstantineau/Glaucus/internal/profile"
)

type Invocation struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ReadFileTool struct{}
type WriteFileTool struct{}
type PatchTool struct{}
type SearchFilesTool struct{}

func RegisterFileTools(registry *Registry) {
	if registry == nil {
		return
	}
	registry.Register(ReadFileTool{})
	registry.Register(WriteFileTool{})
	registry.Register(PatchTool{})
	registry.Register(SearchFilesTool{})
}

func (ReadFileTool) Definition() ToolDefinition    { return mustDefinition("read_file") }
func (WriteFileTool) Definition() ToolDefinition   { return mustDefinition("write_file") }
func (PatchTool) Definition() ToolDefinition       { return mustDefinition("patch") }
func (SearchFilesTool) Definition() ToolDefinition { return mustDefinition("search_files") }

func (ReadFileTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (WriteFileTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (PatchTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (SearchFilesTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	return AvailabilityResult{Available: true}
}

func (ReadFileTool) Execute(_ context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()

	path, ok := getStringArg(req.Arguments, "path")
	if !ok {
		return validationResult("path is required", started)
	}

	target, err := resolveAllowedPath(req.ProfileRoot, req.WorkingDirectory, path)
	if err != nil {
		return fatalResult(err.Error(), started)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return fatalResult(fmt.Sprintf("read file: %v", err), started)
	}
	if isBinary(data) {
		return fatalResult("binary files are not supported by read_file", started)
	}

	lines := splitLines(string(data))
	startLine := getIntArg(req.Arguments, "start_line", 1)
	if startLine < 1 {
		return validationResult("start_line must be at least 1", started)
	}
	limit := getIntArg(req.Arguments, "limit", 200)
	if limit < 1 {
		return validationResult("limit must be at least 1", started)
	}

	startIdx := startLine - 1
	if startIdx > len(lines) {
		startIdx = len(lines)
	}
	endIdx := startIdx + limit
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	content := strings.Join(lines[startIdx:endIdx], "\n")
	display := content
	if display == "" {
		display = "(no content in requested line range)"
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: display,
		Payload: map[string]any{
			"path":        target,
			"start_line":  startLine,
			"end_line":    endIdx,
			"total_lines": len(lines),
			"content":     content,
		},
		Timing: timingSince(started),
	}
}

func (WriteFileTool) Execute(_ context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()

	path, ok := getStringArg(req.Arguments, "path")
	if !ok {
		return validationResult("path is required", started)
	}
	content, ok := getStringArg(req.Arguments, "content")
	if !ok {
		return validationResult("content is required", started)
	}

	target, err := resolveAllowedPath(req.ProfileRoot, req.WorkingDirectory, path)
	if err != nil {
		return fatalResult(err.Error(), started)
	}
	if !utf8.ValidString(content) {
		return validationResult("content must be valid UTF-8 text", started)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fatalResult(fmt.Sprintf("create parent directories: %v", err), started)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fatalResult(fmt.Sprintf("write file: %v", err), started)
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: fmt.Sprintf("Wrote %d bytes to %s.", len(content), target),
		Payload: map[string]any{
			"path":  target,
			"bytes": len(content),
		},
		Timing: timingSince(started),
	}
}

func (PatchTool) Execute(_ context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()

	path, ok := getStringArg(req.Arguments, "path")
	if !ok {
		return validationResult("path is required", started)
	}
	replacement, ok := getStringArg(req.Arguments, "replacement")
	if !ok {
		return validationResult("replacement is required", started)
	}
	startLine := getIntArg(req.Arguments, "start_line", 0)
	endLine := getIntArg(req.Arguments, "end_line", 0)
	if startLine < 1 || endLine < startLine {
		return validationResult("start_line and end_line must define a valid inclusive range", started)
	}

	target, err := resolveAllowedPath(req.ProfileRoot, req.WorkingDirectory, path)
	if err != nil {
		return fatalResult(err.Error(), started)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return fatalResult(fmt.Sprintf("read file: %v", err), started)
	}
	if isBinary(data) {
		return fatalResult("binary files are not supported by patch", started)
	}

	lines := splitLines(string(data))
	if endLine > len(lines) {
		return validationResult(fmt.Sprintf("line range %d-%d is outside file bounds (%d lines)", startLine, endLine, len(lines)), started)
	}

	replacementLines := splitLines(replacement)
	updated := append([]string{}, lines[:startLine-1]...)
	updated = append(updated, replacementLines...)
	updated = append(updated, lines[endLine:]...)

	output := strings.Join(updated, "\n")
	if strings.HasSuffix(string(data), "\n") {
		output += "\n"
	}
	if err := os.WriteFile(target, []byte(output), 0o644); err != nil {
		return fatalResult(fmt.Sprintf("write patched file: %v", err), started)
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: fmt.Sprintf("Patched %s lines %d-%d.", target, startLine, endLine),
		Payload: map[string]any{
			"path":       target,
			"start_line": startLine,
			"end_line":   endLine,
		},
		Timing: timingSince(started),
	}
}

func (SearchFilesTool) Execute(_ context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()

	query, ok := getStringArg(req.Arguments, "query")
	if !ok {
		return validationResult("query is required", started)
	}
	rootArg, _ := getStringArg(req.Arguments, "path")
	limit := getIntArg(req.Arguments, "limit", 20)
	if limit < 1 {
		return validationResult("limit must be at least 1", started)
	}

	searchRoot := req.ProfileRoot
	if strings.TrimSpace(rootArg) != "" {
		target, err := resolveAllowedPath(req.ProfileRoot, req.WorkingDirectory, rootArg)
		if err != nil {
			return fatalResult(err.Error(), started)
		}
		searchRoot = target
	}

	queryLower := strings.ToLower(query)
	matches := make([]map[string]any, 0, limit)
	walkErr := filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if len(matches) >= limit {
			return fs.SkipAll
		}

		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}
		lines := splitLines(string(data))
		for idx, line := range lines {
			if strings.Contains(strings.ToLower(line), queryLower) {
				matches = append(matches, map[string]any{
					"path": path,
					"line": idx + 1,
					"text": line,
				})
				if len(matches) >= limit {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
		return fatalResult(fmt.Sprintf("search files: %v", walkErr), started)
	}

	display := fmt.Sprintf("Found %d matches for %q.", len(matches), query)
	if len(matches) > 0 {
		lines := make([]string, 0, len(matches))
		for _, match := range matches {
			lines = append(lines, fmt.Sprintf("%s:%v: %v", match["path"], match["line"], match["text"]))
		}
		display = strings.Join(lines, "\n")
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: display,
		Payload: map[string]any{
			"query":   query,
			"matches": matches,
		},
		Timing: timingSince(started),
	}
}

func resolveAllowedPath(profileRoot, workingDirectory, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("path is required")
	}

	roots := make([]string, 0, 2)
	if strings.TrimSpace(profileRoot) != "" {
		roots = append(roots, profileRoot)
	}
	if strings.TrimSpace(workingDirectory) != "" && workingDirectory != profileRoot {
		roots = append(roots, workingDirectory)
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("no approved roots are configured")
	}

	if filepath.IsAbs(value) {
		for _, root := range roots {
			target, err := profile.ResolveOwnedPath(root, value)
			if err == nil {
				return target, nil
			}
		}
		return "", fmt.Errorf("path %q is outside approved roots", value)
	}

	base := workingDirectory
	if strings.TrimSpace(base) == "" {
		base = profileRoot
	}
	target, err := profile.ResolveOwnedPath(base, value)
	if err != nil {
		return "", err
	}
	return target, nil
}

func getStringArg(args map[string]any, key string) (string, bool) {
	if args == nil {
		return "", false
	}
	value, ok := args[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func getIntArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func splitLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}

func isBinary(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func timingSince(started time.Time) ToolTiming {
	return ToolTiming{
		StartedAt: started.Format(time.RFC3339Nano),
		EndedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func validationResult(message string, started time.Time) ToolResult {
	return ToolResult{
		Status:      StatusValidationError,
		DisplayText: message,
		Timing:      timingSince(started),
	}
}

func fatalResult(message string, started time.Time) ToolResult {
	return ToolResult{
		Status:      StatusFatalError,
		DisplayText: message,
		Timing:      timingSince(started),
	}
}

func mustDefinition(name string) ToolDefinition {
	registry := NewRegistry()
	RegisterCatalogDefaults(registry)
	tool, ok := registry.Tool(name)
	if !ok {
		panic("missing tool definition: " + name)
	}
	return tool.Definition()
}
