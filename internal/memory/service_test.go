package memory

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/pocketbase/core"
)

func TestWriteAndReadMemoryDocument(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	doc, err := service.WriteDocument(context.Background(), WriteInput{
		ProfileID:    "default",
		ProfileRoot:  activeProfile.Root,
		Slug:         "project-notes",
		Title:        "Project Notes",
		RelativePath: "memories/project-notes.md",
		Content:      "# Notes\nimportant context",
	})
	if err != nil {
		t.Fatalf("write document: %v", err)
	}
	if doc.ID == "" || doc.Checksum == "" {
		t.Fatalf("expected durable memory metadata, got %+v", doc)
	}

	loaded, err := service.GetDocumentBySlug(context.Background(), "default", "project-notes")
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	content, err := service.ReadDocumentContent(activeProfile.Root, loaded)
	if err != nil {
		t.Fatalf("read document content: %v", err)
	}
	if content != "# Notes\nimportant context" {
		t.Fatalf("unexpected document content: %q", content)
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
