package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/skills"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type skillsToolAdapter struct {
	service     *skills.Service
	profileRoot string
}

func (a skillsToolAdapter) ListSkills(ctx context.Context, profileID string, limit int) (any, error) {
	return a.service.ListSkills(ctx, profileID, limit)
}

func (a skillsToolAdapter) ViewSkill(ctx context.Context, profileID, slug string) (any, string, error) {
	skill, err := a.service.GetSkillBySlug(ctx, profileID, slug)
	if err != nil {
		return nil, "", err
	}
	skillRoot, err := profile.ResolveOwnedPath(a.profileRoot, skill.RootPath)
	if err != nil {
		return nil, "", err
	}
	body, err := os.ReadFile(filepath.Join(skillRoot, skill.EntryFile))
	if err != nil {
		return nil, "", fmt.Errorf("read skill body: %w", err)
	}
	_, _ = a.service.TouchUsage(ctx, profileID, slug)
	return skill, string(body), nil
}

func (a skillsToolAdapter) ManageSkill(ctx context.Context, input tools.SkillsManageInput) (any, error) {
	switch input.Action {
	case "install_local", "update_local":
		return a.service.InstallLocal(ctx, skills.InstallInput{
			ProfileID:   input.ProfileID,
			ProfileRoot: input.ProfileRoot,
			Name:        input.Name,
			Slug:        input.Slug,
			SourcePath:  input.SourcePath,
			TrustLevel:  input.TrustLevel,
		})
	case "install_remote", "update_remote":
		return a.service.InstallRemote(ctx, skills.InstallInput{
			ProfileID:   input.ProfileID,
			ProfileRoot: input.ProfileRoot,
			Name:        input.Name,
			Slug:        input.Slug,
			SourceURL:   input.SourceURL,
			TrustLevel:  input.TrustLevel,
		})
	case "pin":
		return a.service.UpdateSkillState(ctx, input.ProfileID, input.Slug, skills.UpdateInput{State: "pinned"})
	case "archive":
		return a.service.UpdateSkillState(ctx, input.ProfileID, input.Slug, skills.UpdateInput{State: "archived"})
	case "activate":
		return a.service.UpdateSkillState(ctx, input.ProfileID, input.Slug, skills.UpdateInput{State: "active"})
	default:
		return nil, fmt.Errorf("unsupported skill action %q", input.Action)
	}
}
