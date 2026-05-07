package app

import (
	"context"
	"errors"
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
