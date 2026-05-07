package exports

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/pocketbase/core"
)

func TestCreateExportAndValidateImportPackage(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	sessionService := sessions.NewService(app)
	session, err := sessionService.CreateSession(context.Background(), sessions.CreateSessionInput{
		ProfileID: "default",
		Source:    "web",
		Title:     "Export test",
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sessionService.CreateMessage(context.Background(), sessions.CreateMessageInput{
		ProfileID:   "default",
		SessionID:   session.ID,
		Role:        "user",
		Content:     sessions.MessageContent{{Type: "input_text", Text: "hello export"}},
		VisibleText: "hello export",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	record, err := service.CreateProfileExport(context.Background(), ExportInput{
		ProfileID:   "default",
		ProfileRoot: activeProfile.Root,
		Kind:        "backup",
		Format:      "zip",
		CreatedBy:   "test",
	})
	if err != nil {
		t.Fatalf("create export: %v", err)
	}
	if record.Path == "" {
		t.Fatalf("expected persisted export record, got %+v", record)
	}

	archivePath := filepath.Join(activeProfile.Root, filepath.FromSlash(record.Path))
	validation, err := service.ValidateImportPackage(archivePath)
	if err != nil {
		t.Fatalf("validate import package: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("expected valid import package, got %+v", validation)
	}

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer reader.Close()

	foundManifest := false
	for _, file := range reader.File {
		if file.Name != "manifest.json" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open manifest: %v", err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		payload := map[string]any{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		foundManifest = payload["profile_id"] == "default"
	}
	if !foundManifest {
		t.Fatal("expected manifest.json with profile_id")
	}
}

func newTestApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})
	t.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012")
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return app
}
