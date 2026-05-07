package exports

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionExports = "exports"

type Record struct {
	ID        string
	ProfileID string
	Kind      string
	Format    string
	Path      string
	Status    string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ExportInput struct {
	ProfileID   string
	ProfileRoot string
	Kind        string
	Format      string
	CreatedBy   string
}

type ValidationResult struct {
	Valid         bool     `json:"valid"`
	Files         []string `json:"files"`
	Missing       []string `json:"missing"`
	TrajectoryOK  bool     `json:"trajectory_ok"`
	ManifestFound bool     `json:"manifest_found"`
}

type Service struct {
	app core.App
	now func() time.Time
}

func NewService(app core.App) *Service {
	return &Service{
		app: app,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateProfileExport(ctx context.Context, input ExportInput) (Record, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Record{}, fmt.Errorf("profile id is required")
	}
	if strings.TrimSpace(input.ProfileRoot) == "" {
		return Record{}, fmt.Errorf("profile root is required")
	}
	kind := firstNonEmpty(input.Kind, "profile_backup")
	format := firstNonEmpty(input.Format, "zip")
	if format != "zip" {
		return Record{}, fmt.Errorf("unsupported export format %q", format)
	}

	exportDir, err := profile.ResolveOwnedPath(input.ProfileRoot, "exports")
	if err != nil {
		return Record{}, err
	}
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return Record{}, fmt.Errorf("ensure exports directory: %w", err)
	}

	filename := fmt.Sprintf("%s-%s.zip", kind, s.now().Format("20060102-150405"))
	targetPath := filepath.Join(exportDir, filename)
	if err := s.writeZipArchive(ctx, input.ProfileID, input.ProfileRoot, targetPath); err != nil {
		return Record{}, err
	}

	record, err := s.newRecord()
	if err != nil {
		return Record{}, err
	}
	record.Set("profile_id", input.ProfileID)
	record.Set("kind", kind)
	record.Set("format", format)
	record.Set("path", filepath.ToSlash(filepath.Join("exports", filename)))
	record.Set("status", "completed")
	record.Set("created_by", input.CreatedBy)
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Record{}, fmt.Errorf("save export record: %w", err)
	}
	return exportRecordFromModel(record), nil
}

func (s *Service) ListExports(ctx context.Context, profileID string, limit int) ([]Record, error) {
	records, err := s.app.FindRecordsByFilter(
		CollectionExports,
		"profile_id = {:profile_id}",
		"-created",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list exports: %w", err)
	}
	result := make([]Record, 0, len(records))
	for _, record := range records {
		result = append(result, exportRecordFromModel(record))
	}
	_ = ctx
	return result, nil
}

func (s *Service) ValidateImportPackage(path string) (ValidationResult, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("open import package: %w", err)
	}
	defer reader.Close()

	files := make([]string, 0, len(reader.File))
	index := map[string]struct{}{}
	for _, file := range reader.File {
		files = append(files, file.Name)
		index[file.Name] = struct{}{}
	}

	required := []string{
		"manifest.json",
		"records/sessions.json",
		"records/jobs.json",
		"records/skills.json",
		"trajectories/runs.jsonl",
	}
	missing := make([]string, 0)
	for _, name := range required {
		if _, ok := index[name]; !ok {
			missing = append(missing, name)
		}
	}
	_, hasManifest := index["manifest.json"]
	_, hasTrajectory := index["trajectories/runs.jsonl"]
	return ValidationResult{
		Valid:         len(missing) == 0,
		Files:         files,
		Missing:       missing,
		TrajectoryOK:  hasTrajectory,
		ManifestFound: hasManifest,
	}, nil
}

func (s *Service) writeZipArchive(ctx context.Context, profileID, profileRoot, targetPath string) error {
	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("create export archive: %w", err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	defer archive.Close()

	collections := map[string]string{
		"records/sessions.json":         "agent_sessions",
		"records/messages.json":         "agent_messages",
		"records/runs.json":             "agent_runs",
		"records/run_events.json":       "agent_run_events",
		"records/jobs.json":             "cron_jobs",
		"records/job_runs.json":         "cron_job_runs",
		"records/skills.json":           "skills",
		"records/memory_documents.json": "memory_documents",
	}

	manifest := map[string]any{
		"profile_id":   profileID,
		"generated_at": s.now().Format(time.RFC3339Nano),
		"collections":  collections,
	}

	manifestBody, _ := json.MarshalIndent(manifest, "", "  ")
	if err := addZipFile(archive, "manifest.json", manifestBody); err != nil {
		return err
	}

	for exportPath, collection := range collections {
		rows, err := s.exportRecords(collection, profileID)
		if err != nil {
			return err
		}
		body, _ := json.MarshalIndent(rows, "", "  ")
		if err := addZipFile(archive, exportPath, body); err != nil {
			return err
		}
	}

	if err := s.addTrajectoryExport(ctx, archive, profileID); err != nil {
		return err
	}

	for _, relativePath := range []string{"config.yaml", "SOUL.md"} {
		if err := addProfileFile(archive, profileRoot, relativePath); err != nil {
			return err
		}
	}
	for _, dir := range []string{"memories", "skills"} {
		if err := addProfileDir(archive, profileRoot, dir); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) exportRecords(collection, profileID string) ([]map[string]any, error) {
	rows, err := s.app.FindRecordsByFilter(collection, "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		// optional collections may not exist or may not use profile_id in legacy fixtures
		if strings.Contains(err.Error(), "no such table") {
			return []map[string]any{}, nil
		}
		return nil, fmt.Errorf("export records %s: %w", collection, err)
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		exported := map[string]any{
			"id":      row.Id,
			"created": row.GetDateTime("created").Time(),
			"updated": row.GetDateTime("updated").Time(),
		}
		for _, field := range row.Collection().Fields {
			exported[field.GetName()] = row.Get(field.GetName())
		}
		result = append(result, exported)
	}
	return result, nil
}

func (s *Service) addTrajectoryExport(ctx context.Context, archive *zip.Writer, profileID string) error {
	runs, err := s.app.FindRecordsByFilter("agent_runs", "profile_id = {:profile_id}", "", 0, 0, dbx.Params{"profile_id": profileID})
	if err != nil {
		return fmt.Errorf("load runs for trajectories: %w", err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	for _, run := range runs {
		events, err := s.app.FindRecordsByFilter("agent_run_events", "run_id = {:run_id}", "sequence", 0, 0, dbx.Params{"run_id": run.Id})
		if err != nil {
			return fmt.Errorf("load events for trajectory: %w", err)
		}
		messages, err := s.app.FindRecordsByFilter("agent_messages", "run_id = {:run_id}", "ordinal", 0, 0, dbx.Params{"run_id": run.Id})
		if err != nil {
			return fmt.Errorf("load messages for trajectory: %w", err)
		}
		payload := map[string]any{
			"run_id":       run.Id,
			"profile_id":   profileID,
			"session_id":   run.GetString("session_id"),
			"status":       run.GetString("status"),
			"request":      run.GetString("request_json"),
			"resolution":   run.GetString("provider_resolution_json"),
			"tool_calls":   messages,
			"events":       events,
			"generated_at": s.now().Format(time.RFC3339Nano),
		}
		if err := encoder.Encode(payload); err != nil {
			return fmt.Errorf("encode trajectory row: %w", err)
		}
	}
	return addZipFile(archive, "trajectories/runs.jsonl", buffer.Bytes())
}

func (s *Service) newRecord() (*core.Record, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollectionExports)
	if err != nil {
		return nil, fmt.Errorf("find exports collection: %w", err)
	}
	return core.NewRecord(collection), nil
}

func exportRecordFromModel(record *core.Record) Record {
	return Record{
		ID:        record.Id,
		ProfileID: record.GetString("profile_id"),
		Kind:      record.GetString("kind"),
		Format:    record.GetString("format"),
		Path:      record.GetString("path"),
		Status:    record.GetString("status"),
		CreatedBy: record.GetString("created_by"),
		CreatedAt: record.GetDateTime("created").Time(),
		UpdatedAt: record.GetDateTime("updated").Time(),
	}
}

func addZipFile(archive *zip.Writer, name string, body []byte) error {
	writer, err := archive.Create(name)
	if err != nil {
		return fmt.Errorf("create zip file %s: %w", name, err)
	}
	if _, err := writer.Write(body); err != nil {
		return fmt.Errorf("write zip file %s: %w", name, err)
	}
	return nil
}

func addProfileFile(archive *zip.Writer, profileRoot, relativePath string) error {
	absolutePath := filepath.Join(profileRoot, relativePath)
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read profile file %s: %w", relativePath, err)
	}
	return addZipFile(archive, filepath.ToSlash("profile/"+relativePath), data)
}

func addProfileDir(archive *zip.Writer, profileRoot, relativeDir string) error {
	absoluteDir := filepath.Join(profileRoot, relativeDir)
	return filepath.Walk(absoluteDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(profileRoot, path)
		if err != nil {
			return err
		}
		return addZipFile(archive, filepath.ToSlash("profile/"+relativePath), data)
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
