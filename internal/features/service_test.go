package features

import (
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestReconcilePersistsAdvancedFeatureContracts(t *testing.T) {
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

	service := NewService(app)
	if err := service.Reconcile(t.Context()); err != nil {
		t.Fatalf("reconcile contracts: %v", err)
	}

	contracts, err := service.ListContracts(t.Context(), 10)
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if len(contracts) < 5 {
		t.Fatalf("expected advanced contracts to persist, got %d", len(contracts))
	}
}
