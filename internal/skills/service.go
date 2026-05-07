package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionSkills = "skills"

type Skill struct {
	ID          string
	ProfileID   string
	Name        string
	Slug        string
	Version     string
	Description string
	State       string
	TrustLevel  string
	Provenance  map[string]any
	RootPath    string
	EntryFile   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type InstallInput struct {
	ProfileID   string
	ProfileRoot string
	Name        string
	Slug        string
	SourcePath  string
	SourceURL   string
	TrustLevel  string
}

type UpdateInput struct {
	State      string
	TrustLevel string
}

type Service struct {
	app    core.App
	client *http.Client
	now    func() time.Time
}

func NewService(app core.App) *Service {
	return &Service{
		app:    app,
		client: &http.Client{Timeout: 30 * time.Second},
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) ListSkills(ctx context.Context, profileID string, limit int) ([]Skill, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionSkills,
		"profile_id = {:profile_id}",
		"slug",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	result := make([]Skill, 0, len(records))
	for _, record := range records {
		skill, err := skillFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, skill)
	}
	_ = ctx
	return result, nil
}

func (s *Service) GetSkillBySlug(ctx context.Context, profileID, slug string) (Skill, error) {
	record, err := s.app.FindFirstRecordByFilter(
		CollectionSkills,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": profileID, "slug": slug},
	)
	if err != nil {
		return Skill{}, fmt.Errorf("find skill: %w", err)
	}
	_ = ctx
	return skillFromRecord(record)
}

func (s *Service) InstallLocal(ctx context.Context, input InstallInput) (Skill, error) {
	if strings.TrimSpace(input.SourcePath) == "" {
		return Skill{}, errors.New("source path is required")
	}
	sourcePath, err := filepath.Abs(input.SourcePath)
	if err != nil {
		return Skill{}, fmt.Errorf("resolve source path: %w", err)
	}
	meta, err := validateSkillDir(sourcePath)
	if err != nil {
		return Skill{}, err
	}
	return s.installFromLocal(ctx, input, meta, sourcePath)
}

func (s *Service) InstallRemote(ctx context.Context, input InstallInput) (Skill, error) {
	if strings.TrimSpace(input.SourceURL) == "" {
		return Skill{}, errors.New("source url is required")
	}
	resp, err := s.client.Get(input.SourceURL)
	if err != nil {
		return Skill{}, fmt.Errorf("fetch remote skill: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Skill{}, fmt.Errorf("fetch remote skill: received status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Skill{}, fmt.Errorf("read remote skill: %w", err)
	}

	name := strings.TrimSpace(input.Name)
	slug := strings.TrimSpace(input.Slug)
	if slug == "" {
		slug = slugify(firstNonEmpty(name, "remote-skill"))
	}
	if name == "" {
		name = strings.ReplaceAll(slug, "-", " ")
	}

	targetDir, err := profile.ResolveOwnedPath(input.ProfileRoot, filepath.Join("skills", slug))
	if err != nil {
		return Skill{}, err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("create remote skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), body, 0o644); err != nil {
		return Skill{}, fmt.Errorf("write remote skill: %w", err)
	}

	meta := skillMetadata{
		Name:        name,
		Slug:        slug,
		Version:     "",
		Description: firstNonEmpty(readFirstLine(string(body)), "Remote skill"),
		EntryFile:   "SKILL.md",
	}
	return s.upsertSkillRecord(ctx, input.ProfileID, input.ProfileRoot, targetDir, meta, map[string]any{
		"source":       "remote",
		"source_url":   input.SourceURL,
		"installed_at": s.now().Format(time.RFC3339Nano),
		"last_used_at": s.now().Format(time.RFC3339Nano),
		"usage_count":  1,
	})
}

func (s *Service) UpdateSkillState(ctx context.Context, profileID, slug string, input UpdateInput) (Skill, error) {
	record, err := s.app.FindFirstRecordByFilter(
		CollectionSkills,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": profileID, "slug": slug},
	)
	if err != nil {
		return Skill{}, fmt.Errorf("find skill: %w", err)
	}
	if strings.TrimSpace(input.State) != "" {
		record.Set("state", input.State)
	}
	if strings.TrimSpace(input.TrustLevel) != "" {
		record.Set("trust_level", input.TrustLevel)
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Skill{}, fmt.Errorf("update skill: %w", err)
	}
	return skillFromRecord(record)
}

func (s *Service) TouchUsage(ctx context.Context, profileID, slug string) (Skill, error) {
	record, err := s.app.FindFirstRecordByFilter(
		CollectionSkills,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": profileID, "slug": slug},
	)
	if err != nil {
		return Skill{}, fmt.Errorf("find skill: %w", err)
	}
	skill, err := skillFromRecord(record)
	if err != nil {
		return Skill{}, err
	}
	if skill.Provenance == nil {
		skill.Provenance = map[string]any{}
	}
	skill.Provenance["last_used_at"] = s.now().Format(time.RFC3339Nano)
	skill.Provenance["usage_count"] = intValue(skill.Provenance["usage_count"]) + 1
	if err := setJSON(record, "provenance_json", skill.Provenance); err != nil {
		return Skill{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Skill{}, fmt.Errorf("touch skill usage: %w", err)
	}
	return skillFromRecord(record)
}

func (s *Service) ReconcileLifecycle(ctx context.Context) (int, error) {
	records, err := s.app.FindRecordsByFilter(CollectionSkills, "id != ''", "", 0, 0)
	if err != nil {
		return 0, fmt.Errorf("list skills for reconcile: %w", err)
	}
	updated := 0
	for _, record := range records {
		skill, err := skillFromRecord(record)
		if err != nil {
			return updated, err
		}
		if skill.State == "archived" || skill.State == "pinned" {
			continue
		}
		lastUsed := parseTime(skill.Provenance["last_used_at"])
		nextState := "stale"
		if !lastUsed.IsZero() && s.now().Sub(lastUsed) <= 7*24*time.Hour {
			nextState = "active"
		}
		if skill.State == nextState {
			continue
		}
		record.Set("state", nextState)
		if err := s.app.SaveWithContext(ctx, record); err != nil {
			return updated, fmt.Errorf("save reconciled skill: %w", err)
		}
		updated++
	}
	return updated, nil
}

func (s *Service) installFromLocal(ctx context.Context, input InstallInput, meta skillMetadata, sourcePath string) (Skill, error) {
	targetDir, err := profile.ResolveOwnedPath(input.ProfileRoot, filepath.Join("skills", meta.Slug))
	if err != nil {
		return Skill{}, err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return Skill{}, fmt.Errorf("clear existing skill directory: %w", err)
	}
	if err := copyDir(sourcePath, targetDir); err != nil {
		return Skill{}, err
	}
	return s.upsertSkillRecord(ctx, input.ProfileID, input.ProfileRoot, targetDir, meta, map[string]any{
		"source":       "local",
		"source_path":  sourcePath,
		"installed_at": s.now().Format(time.RFC3339Nano),
		"last_used_at": s.now().Format(time.RFC3339Nano),
		"usage_count":  1,
	})
}

func (s *Service) upsertSkillRecord(ctx context.Context, profileID, profileRoot, absoluteRoot string, meta skillMetadata, provenance map[string]any) (Skill, error) {
	existing, _ := s.app.FindFirstRecordByFilter(
		CollectionSkills,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": profileID, "slug": meta.Slug},
	)

	var record *core.Record
	if existing != nil {
		record = existing
	} else {
		collection, err := s.app.FindCollectionByNameOrId(CollectionSkills)
		if err != nil {
			return Skill{}, fmt.Errorf("find skills collection: %w", err)
		}
		record = core.NewRecord(collection)
		record.Set("profile_id", profileID)
		record.Set("slug", meta.Slug)
	}

	relativeRoot, err := filepath.Rel(profileRoot, absoluteRoot)
	if err != nil {
		return Skill{}, fmt.Errorf("relative skill path: %w", err)
	}

	record.Set("name", meta.Name)
	record.Set("version", meta.Version)
	record.Set("description", meta.Description)
	record.Set("state", firstNonEmpty(record.GetString("state"), "active"))
	record.Set("trust_level", firstNonEmpty(strings.TrimSpace(record.GetString("trust_level")), firstNonEmpty(meta.TrustLevel, "trusted")))
	record.Set("root_path", filepath.ToSlash(relativeRoot))
	record.Set("entry_file", meta.EntryFile)
	if err := setJSON(record, "provenance_json", provenance); err != nil {
		return Skill{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Skill{}, fmt.Errorf("save skill record: %w", err)
	}
	return skillFromRecord(record)
}

type skillMetadata struct {
	Name        string
	Slug        string
	Version     string
	Description string
	EntryFile   string
	TrustLevel  string
}

func validateSkillDir(root string) (skillMetadata, error) {
	info, err := os.Stat(filepath.Join(root, "SKILL.md"))
	if err != nil || info.IsDir() {
		return skillMetadata{}, errors.New("skill directory must contain SKILL.md")
	}
	name := filepath.Base(root)
	return skillMetadata{
		Name:        strings.ReplaceAll(name, "-", " "),
		Slug:        slugify(name),
		Description: "Local skill",
		EntryFile:   "SKILL.md",
	}, nil
}

func copyDir(source, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("create target skill directory: %w", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read skill directory: %w", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyDir(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("read skill file: %w", err)
		}
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return fmt.Errorf("write skill file: %w", err)
		}
	}
	return nil
}

func skillFromRecord(record *core.Record) (Skill, error) {
	skill := Skill{
		ID:          record.Id,
		ProfileID:   record.GetString("profile_id"),
		Name:        record.GetString("name"),
		Slug:        record.GetString("slug"),
		Version:     record.GetString("version"),
		Description: record.GetString("description"),
		State:       record.GetString("state"),
		TrustLevel:  record.GetString("trust_level"),
		RootPath:    record.GetString("root_path"),
		EntryFile:   record.GetString("entry_file"),
		CreatedAt:   record.GetDateTime("created").Time(),
		UpdatedAt:   record.GetDateTime("updated").Time(),
	}
	if err := decodeJSONField(record, "provenance_json", &skill.Provenance); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func setJSON(record *core.Record, field string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", field, err)
	}
	record.Set(field, string(raw))
	return nil
}

func decodeJSONField(record *core.Record, field string, target any) error {
	raw := record.GetString(field)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readFirstLine(value string) string {
	line := strings.TrimSpace(strings.Split(value, "\n")[0])
	line = strings.TrimPrefix(line, "#")
	return strings.TrimSpace(line)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func parseTime(value any) time.Time {
	text, _ := value.(string)
	parsed, _ := time.Parse(time.RFC3339Nano, text)
	return parsed
}
