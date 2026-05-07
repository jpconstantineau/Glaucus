package skills

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/pocketbase/core"
)

func TestInstallLocalAndRemoteSkillsAndReconcileLifecycle(t *testing.T) {
	app := newTestApp(t)
	service := NewService(app)
	service.now = func() time.Time {
		return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	}
	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: t.TempDir(),
		Slug:    "default",
	})
	if err != nil {
		t.Fatalf("bootstrap profile: %v", err)
	}

	sourceDir := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte("# Demo Skill\n"), 0o644); err != nil {
		t.Fatalf("write source skill: %v", err)
	}

	localSkill, err := service.InstallLocal(context.Background(), InstallInput{
		ProfileID:   "default",
		ProfileRoot: activeProfile.Root,
		SourcePath:  sourceDir,
	})
	if err != nil {
		t.Fatalf("install local skill: %v", err)
	}
	if localSkill.Slug == "" || localSkill.RootPath == "" {
		t.Fatalf("expected persisted local skill metadata, got %+v", localSkill)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Remote Skill\nRemote body"))
	}))
	defer server.Close()

	remoteSkill, err := service.InstallRemote(context.Background(), InstallInput{
		ProfileID:   "default",
		ProfileRoot: activeProfile.Root,
		Name:        "Remote Skill",
		Slug:        "remote-skill",
		SourceURL:   server.URL,
	})
	if err != nil {
		t.Fatalf("install remote skill: %v", err)
	}
	if remoteSkill.Slug != "remote-skill" {
		t.Fatalf("expected remote skill slug, got %+v", remoteSkill)
	}

	if _, err := service.TouchUsage(context.Background(), "default", localSkill.Slug); err != nil {
		t.Fatalf("touch usage: %v", err)
	}

	service.now = func() time.Time {
		return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	}
	updated, err := service.ReconcileLifecycle(context.Background())
	if err != nil {
		t.Fatalf("reconcile lifecycle: %v", err)
	}
	if updated == 0 {
		t.Fatal("expected curator reconcile to update at least one skill state")
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
