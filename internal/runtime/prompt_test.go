package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jpconstantineau/Glaucus/internal/goals"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
)

func TestPromptBuilderOrdersStagesAndLoadsProfileInputs(t *testing.T) {
	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	if err := os.WriteFile(filepath.Join(activeProfile.Root, "SOUL.md"), []byte("Identity block"), 0o644); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeProfile.Root, "memories", "MEMORY.md"), []byte("Long-term memory"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeProfile.Root, "memories", "USER.md"), []byte("User profile snapshot"), 0o644); err != nil {
		t.Fatalf("write USER.md: %v", err)
	}
	if err := os.Mkdir(filepath.Join(activeProfile.Root, "skills", "triage"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}

	builder := NewPromptBuilder()
	doc, err := builder.Build(PromptBuildInput{
		Profile: activeProfile,
		Session: sessions.Session{
			ID:            "sess_123",
			Source:        "web",
			Title:         "Investigate dashboard issue",
			Status:        "active",
			ModelSnapshot: map[string]any{"provider": "openrouter", "model": "openai/gpt-4.1-mini"},
		},
		ToolBehavior:    "Use the safe-default toolset.",
		ProviderOverlay: "Prefer low-latency models first.",
		SystemOverride:  "Respond tersely.",
		SessionGoals: []goals.Goal{{
			Title:           "Fix dashboard issue",
			Statement:       "Keep the active incident contained.",
			SuccessCriteria: "Bug is reproduced and covered by a test.",
			Status:          "active",
			Priority:        "high",
		}},
		ProfileGoals: []goals.Goal{{
			Title:          "Preserve review clarity",
			Statement:      "Explain changes in a way operators can audit.",
			Status:         "active",
			Priority:       "medium",
			LastEvaluation: map[string]any{"summary": "Previous slice handoff was clean."},
		}},
		ProjectContext: "Repository root is C:/GIT/Glaucus.",
		PlatformHint:   "The request originated from the browser UI.",
	})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}

	var gotNames []string
	for _, fragment := range doc.Fragments {
		gotNames = append(gotNames, fragment.Name)
	}
	wantNames := []string{
		"identity",
		"tool behavior",
		"provider overlay",
		"system override",
		"memory snapshot",
		"user profile snapshot",
		"skills index",
		"goals",
		"project context",
		"session metadata",
		"platform hint",
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("unexpected fragment order: got %v want %v", gotNames, wantNames)
	}

	rendered := RenderPrompt(doc)
	for _, expected := range []string{
		"Identity block",
		"Long-term memory",
		"User profile snapshot",
		"triage",
		"Session goals:",
		"Previous slice handoff was clean.",
		"Selected Provider: openrouter",
		"The request originated from the browser UI.",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered prompt to contain %q, got:\n%s", expected, rendered)
		}
	}
}

func TestPromptBuilderOmitsMissingOptionalInputs(t *testing.T) {
	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	if err := os.Remove(filepath.Join(activeProfile.Root, "memories", "MEMORY.md")); err != nil {
		t.Fatalf("remove MEMORY.md: %v", err)
	}
	if err := os.Remove(filepath.Join(activeProfile.Root, "memories", "USER.md")); err != nil {
		t.Fatalf("remove USER.md: %v", err)
	}

	builder := NewPromptBuilder()
	doc, err := builder.Build(PromptBuildInput{
		Profile: activeProfile,
		Session: sessions.Session{
			ID:     "sess_omit",
			Source: "web",
			Title:  "Minimal prompt",
			Status: "active",
		},
	})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}

	var names []string
	for _, fragment := range doc.Fragments {
		names = append(names, fragment.Name)
	}
	if slices.Contains(names, "memory snapshot") || slices.Contains(names, "user profile snapshot") || slices.Contains(names, "system override") {
		t.Fatalf("expected missing optional inputs to be omitted, got %v", names)
	}

	if !slices.Contains(doc.Diagnostics, "MEMORY.md missing") || !slices.Contains(doc.Diagnostics, "USER.md missing") {
		t.Fatalf("expected omission diagnostics, got %v", doc.Diagnostics)
	}
}
