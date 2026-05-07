package tools

import (
	"context"
	"testing"
)

func TestSkillsTools(t *testing.T) {
	manager := &fakeSkillsManager{}
	listTool := SkillsListTool{manager: manager}
	viewTool := SkillViewTool{manager: manager}
	manageTool := SkillManageTool{manager: manager}

	if result := listTool.Execute(context.Background(), ToolRequest{ProfileID: "default"}); result.Status != StatusSuccess {
		t.Fatalf("expected skills_list success, got %s", result.Status)
	}
	if result := viewTool.Execute(context.Background(), ToolRequest{
		ProfileID: "default",
		Arguments: map[string]any{"slug": "demo"},
	}); result.Status != StatusSuccess {
		t.Fatalf("expected skill_view success, got %s", result.Status)
	}
	if result := manageTool.Execute(context.Background(), ToolRequest{
		ProfileID:   "default",
		ProfileRoot: t.TempDir(),
		Arguments: map[string]any{
			"action": "pin",
			"slug":   "demo",
		},
	}); result.Status != StatusSuccess {
		t.Fatalf("expected skill_manage success, got %s", result.Status)
	}
}

type fakeSkillsManager struct{}

func (f *fakeSkillsManager) ListSkills(ctx context.Context, profileID string, limit int) (any, error) {
	_ = ctx
	_ = profileID
	_ = limit
	return []map[string]any{{"slug": "demo"}}, nil
}

func (f *fakeSkillsManager) ViewSkill(ctx context.Context, profileID, slug string) (any, string, error) {
	_ = ctx
	_ = profileID
	return map[string]any{"slug": slug}, "# Demo", nil
}

func (f *fakeSkillsManager) ManageSkill(ctx context.Context, input SkillsManageInput) (any, error) {
	_ = ctx
	return map[string]any{"slug": input.Slug, "state": input.State}, nil
}
