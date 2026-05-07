package migrations_test

import (
	"os"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
)

func TestRunAllMigrationsCreatesCoreCollectionsAndIsIdempotent(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "GLAUCUS_TEST_ENCRYPTION_KEY",
	})

	if err := os.Setenv("GLAUCUS_TEST_ENCRYPTION_KEY", "12345678901234567890123456789012"); err != nil {
		t.Fatalf("set encryption env: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("GLAUCUS_TEST_ENCRYPTION_KEY")
	})
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}

	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations a second time: %v", err)
	}

	requiredCollections := []string{
		"profiles",
		"operators",
		"agent_sessions",
		"agent_messages",
		"agent_runs",
		"agent_run_events",
		"approval_requests",
		"cron_jobs",
		"cron_job_runs",
		"skills",
		"toolsets",
		"provider_profiles",
		"platform_adapters",
		"background_processes",
		"exports",
	}

	for _, name := range requiredCollections {
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("expected collection %s: %v", name, err)
		}
		if len(collection.Indexes) == 0 && name != "operators" {
			t.Fatalf("expected collection %s to define at least one index", name)
		}
	}

	sessions, err := app.FindCollectionByNameOrId("agent_sessions")
	if err != nil {
		t.Fatalf("load sessions collection: %v", err)
	}
	if sessions.Fields.GetByName("profile_id") == nil {
		t.Fatal("expected agent_sessions.profile_id field")
	}
}
