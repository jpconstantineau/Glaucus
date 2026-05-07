package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type stubService struct {
	name     string
	startErr error
	stopErr  error
	started  *[]string
	stopped  *[]string
}

func (s stubService) Name() string { return s.name }

func (s stubService) Start(context.Context) error {
	if s.started != nil {
		*s.started = append(*s.started, s.name)
	}
	return s.startErr
}

func (s stubService) Stop(context.Context) error {
	if s.stopped != nil {
		*s.stopped = append(*s.stopped, s.name)
	}
	return s.stopErr
}

func TestLifecycleStartsAndStopsInOrder(t *testing.T) {
	var started []string
	var stopped []string

	lifecycle := NewLifecycle(
		stubService{name: "first", started: &started, stopped: &stopped},
		stubService{name: "second", started: &started, stopped: &stopped},
	)

	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("start lifecycle: %v", err)
	}

	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("stop lifecycle: %v", err)
	}

	if !reflect.DeepEqual(started, []string{"first", "second"}) {
		t.Fatalf("unexpected start order: %v", started)
	}

	if !reflect.DeepEqual(stopped, []string{"second", "first"}) {
		t.Fatalf("unexpected stop order: %v", stopped)
	}
}

func TestLifecycleStopsStartedServicesWhenStartupFails(t *testing.T) {
	var stopped []string
	expected := errors.New("boom")

	lifecycle := NewLifecycle(
		stubService{name: "first", stopped: &stopped},
		stubService{name: "second", startErr: expected, stopped: &stopped},
	)

	err := lifecycle.Start(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected start error %v, got %v", expected, err)
	}

	if !reflect.DeepEqual(stopped, []string{"first"}) {
		t.Fatalf("unexpected stopped services: %v", stopped)
	}
}

func TestNewRuntimeRequiresName(t *testing.T) {
	if _, err := NewRuntime(RuntimeOptions{}); err == nil {
		t.Fatal("expected runtime creation to fail without a name")
	}
}

func TestNewRuntimeBootstrapsProfileAndLoadsConfig(t *testing.T) {
	profilesDir := t.TempDir()
	profileRoot := filepath.Join(profilesDir, "operator")

	if err := os.MkdirAll(profileRoot, 0o755); err != nil {
		t.Fatalf("create profile root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "config.yaml"), []byte("model:\n  defaultProvider: test-provider\n  defaultModel: test-model\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	runtime, err := NewRuntime(RuntimeOptions{
		Name:        "Glaucus",
		ProfilesDir: profilesDir,
		ProfileSlug: "operator",
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	if runtime.profile.Slug != "operator" {
		t.Fatalf("expected operator profile, got %q", runtime.profile.Slug)
	}
	if runtime.config.Config.Model.DefaultProvider != "test-provider" {
		t.Fatalf("expected provider override, got %q", runtime.config.Config.Model.DefaultProvider)
	}
}
