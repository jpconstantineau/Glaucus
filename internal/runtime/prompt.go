package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
)

type PromptFragment struct {
	Name        string
	Priority    int
	Cacheable   bool
	Content     string
	Diagnostics []string
}

type PromptBuildInput struct {
	Profile         profile.ActiveProfile
	Session         sessions.Session
	ToolBehavior    string
	ProviderOverlay string
	SystemOverride  string
	ProjectContext  string
	PlatformHint    string
}

type PromptDocument struct {
	Fragments   []PromptFragment
	Diagnostics []string
}

type PromptBuilder struct{}

func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

func (b *PromptBuilder) Build(input PromptBuildInput) (PromptDocument, error) {
	stages := []func(PromptBuildInput) (PromptFragment, bool, error){
		b.identityStage,
		b.toolBehaviorStage,
		b.providerOverlayStage,
		b.systemOverrideStage,
		b.memorySnapshotStage,
		b.userProfileStage,
		b.skillsIndexStage,
		b.projectContextStage,
		b.sessionMetadataStage,
		b.platformHintStage,
	}

	fragments := make([]PromptFragment, 0, len(stages))
	diagnostics := make([]string, 0, len(stages))
	for _, stage := range stages {
		fragment, include, err := stage(input)
		if err != nil {
			return PromptDocument{}, err
		}
		diagnostics = append(diagnostics, fragment.Diagnostics...)
		if include {
			fragments = append(fragments, fragment)
		}
	}

	sort.SliceStable(fragments, func(i, j int) bool {
		return fragments[i].Priority < fragments[j].Priority
	})

	return PromptDocument{
		Fragments:   fragments,
		Diagnostics: diagnostics,
	}, nil
}

func RenderPrompt(doc PromptDocument) string {
	sections := make([]string, 0, len(doc.Fragments))
	for _, fragment := range doc.Fragments {
		sections = append(sections, "## "+fragment.Name+"\n"+fragment.Content)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func (b *PromptBuilder) identityStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content, diagnostics, err := readTrimmed(filepath.Join(input.Profile.Root, "SOUL.md"))
	if err != nil {
		return PromptFragment{}, false, fmt.Errorf("identity stage: %w", err)
	}
	if content == "" {
		return PromptFragment{Name: "identity", Priority: 10, Cacheable: true, Diagnostics: diagnostics}, false, nil
	}
	return PromptFragment{Name: "identity", Priority: 10, Cacheable: true, Content: content, Diagnostics: diagnostics}, true, nil
}

func (b *PromptBuilder) toolBehaviorStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content := strings.TrimSpace(input.ToolBehavior)
	if content == "" {
		return PromptFragment{Name: "tool behavior", Priority: 20, Cacheable: true}, false, nil
	}
	return PromptFragment{Name: "tool behavior", Priority: 20, Cacheable: true, Content: content}, true, nil
}

func (b *PromptBuilder) providerOverlayStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content := strings.TrimSpace(input.ProviderOverlay)
	if content == "" {
		return PromptFragment{Name: "provider overlay", Priority: 30, Cacheable: true}, false, nil
	}
	return PromptFragment{Name: "provider overlay", Priority: 30, Cacheable: true, Content: content}, true, nil
}

func (b *PromptBuilder) systemOverrideStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content := strings.TrimSpace(input.SystemOverride)
	if content == "" {
		return PromptFragment{Name: "system override", Priority: 40, Cacheable: false}, false, nil
	}
	return PromptFragment{Name: "system override", Priority: 40, Cacheable: false, Content: content}, true, nil
}

func (b *PromptBuilder) memorySnapshotStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content, diagnostics, err := readTrimmed(filepath.Join(input.Profile.Root, "memories", "MEMORY.md"))
	if err != nil {
		return PromptFragment{}, false, fmt.Errorf("memory stage: %w", err)
	}
	if content == "" {
		return PromptFragment{Name: "memory snapshot", Priority: 50, Cacheable: false, Diagnostics: diagnostics}, false, nil
	}
	return PromptFragment{Name: "memory snapshot", Priority: 50, Cacheable: false, Content: content, Diagnostics: diagnostics}, true, nil
}

func (b *PromptBuilder) userProfileStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content, diagnostics, err := readTrimmed(filepath.Join(input.Profile.Root, "memories", "USER.md"))
	if err != nil {
		return PromptFragment{}, false, fmt.Errorf("user profile stage: %w", err)
	}
	if content == "" {
		return PromptFragment{Name: "user profile snapshot", Priority: 60, Cacheable: false, Diagnostics: diagnostics}, false, nil
	}
	return PromptFragment{Name: "user profile snapshot", Priority: 60, Cacheable: false, Content: content, Diagnostics: diagnostics}, true, nil
}

func (b *PromptBuilder) skillsIndexStage(input PromptBuildInput) (PromptFragment, bool, error) {
	skillsDir := filepath.Join(input.Profile.Root, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return PromptFragment{Name: "skills index", Priority: 70, Cacheable: false, Diagnostics: []string{"skills directory missing"}}, false, nil
		}
		return PromptFragment{}, false, fmt.Errorf("skills index stage: %w", err)
	}

	var skills []string
	for _, entry := range entries {
		if entry.IsDir() {
			skills = append(skills, entry.Name())
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") || strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			skills = append(skills, entry.Name())
		}
	}
	sort.Strings(skills)
	if len(skills) == 0 {
		return PromptFragment{Name: "skills index", Priority: 70, Cacheable: false, Diagnostics: []string{"no skills discovered"}}, false, nil
	}
	return PromptFragment{
		Name:      "skills index",
		Priority:  70,
		Cacheable: false,
		Content:   "Available skills:\n- " + strings.Join(skills, "\n- "),
	}, true, nil
}

func (b *PromptBuilder) projectContextStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content := strings.TrimSpace(input.ProjectContext)
	if content == "" {
		return PromptFragment{Name: "project context", Priority: 80, Cacheable: false}, false, nil
	}
	return PromptFragment{Name: "project context", Priority: 80, Cacheable: false, Content: content}, true, nil
}

func (b *PromptBuilder) sessionMetadataStage(input PromptBuildInput) (PromptFragment, bool, error) {
	if strings.TrimSpace(input.Session.ID) == "" {
		return PromptFragment{Name: "session metadata", Priority: 90, Cacheable: false}, false, nil
	}

	lines := []string{
		"Session ID: " + input.Session.ID,
		"Source: " + input.Session.Source,
		"Title: " + input.Session.Title,
		"Status: " + input.Session.Status,
	}
	if input.Session.ParentSessionID != "" {
		lines = append(lines, "Parent Session ID: "+input.Session.ParentSessionID)
	}
	if provider, ok := input.Session.ModelSnapshot["provider"].(string); ok && provider != "" {
		lines = append(lines, "Selected Provider: "+provider)
	}
	if model, ok := input.Session.ModelSnapshot["model"].(string); ok && model != "" {
		lines = append(lines, "Selected Model: "+model)
	}

	return PromptFragment{
		Name:      "session metadata",
		Priority:  90,
		Cacheable: false,
		Content:   strings.Join(lines, "\n"),
	}, true, nil
}

func (b *PromptBuilder) platformHintStage(input PromptBuildInput) (PromptFragment, bool, error) {
	content := strings.TrimSpace(input.PlatformHint)
	if content == "" {
		return PromptFragment{Name: "platform hint", Priority: 100, Cacheable: false}, false, nil
	}
	return PromptFragment{Name: "platform hint", Priority: 100, Cacheable: false, Content: content}, true, nil
}

func readTrimmed(path string) (string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", []string{filepath.Base(path) + " missing"}, nil
		}
		return "", nil, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", []string{filepath.Base(path) + " empty"}, nil
	}
	return content, nil, nil
}
