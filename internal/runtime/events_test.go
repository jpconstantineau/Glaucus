package runtime

import (
	"context"
	"testing"

	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/pocketbase/pocketbase/core"
)

func TestEventServiceAppendListAndSubscribe(t *testing.T) {
	app := newEventTestApp(t)
	service := NewEventService(app)

	runCh, cancelRun := service.SubscribeRun("run_1")
	defer cancelRun()
	sessionCh, cancelSession := service.SubscribeSession("session_1")
	defer cancelSession()

	event, err := service.Append(context.Background(), AppendEventInput{
		ProfileID:  "profile_default",
		RunID:      "run_1",
		SessionID:  "session_1",
		Type:       "run.started",
		Payload:    map[string]any{"status": "running"},
		IsTerminal: false,
	})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if event.Sequence != 1 {
		t.Fatalf("expected first event sequence to be 1, got %d", event.Sequence)
	}

	select {
	case delivered := <-runCh:
		if delivered.Type != "run.started" {
			t.Fatalf("unexpected run stream event: %#v", delivered)
		}
	default:
		t.Fatal("expected run subscription delivery")
	}

	select {
	case delivered := <-sessionCh:
		if delivered.SessionID != "session_1" {
			t.Fatalf("unexpected session stream event: %#v", delivered)
		}
	default:
		t.Fatal("expected session subscription delivery")
	}

	events, err := service.ListRunEvents(context.Background(), "run_1", 0)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) != 1 || events[0].Payload["status"] != "running" {
		t.Fatalf("unexpected persisted events: %#v", events)
	}
}

func newEventTestApp(t *testing.T) core.App {
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
