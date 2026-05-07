package tools

import (
	"context"
	"errors"
	"testing"
)

type testBrowser struct {
	err error
}

func (b testBrowser) Name() string { return "test-browser" }
func (b testBrowser) Healthy(context.Context) error {
	return b.err
}

func TestRegistryResolveUsesSurfaceDefaultAndAvailability(t *testing.T) {
	registry := NewRegistry()
	RegisterCatalogDefaults(registry)

	resolution := registry.Resolve(context.Background(), ResolveRequest{
		Surface:     SurfaceWebChat,
		ProfileRoot: t.TempDir(),
	})

	if resolution.RequestedToolset != SurfaceWebChat {
		t.Fatalf("expected web chat default toolset, got %q", resolution.RequestedToolset)
	}
	if len(resolution.EnabledTools) == 0 {
		t.Fatal("expected some enabled tools")
	}
	if _, ok := resolution.Availability["browser_navigate"]; !ok {
		t.Fatal("expected browser tool to report availability reason when backend is absent")
	}
}

func TestRegistryResolveHonorsExplicitDisables(t *testing.T) {
	registry := NewRegistry()
	RegisterCatalogDefaults(registry)

	resolution := registry.Resolve(context.Background(), ResolveRequest{
		Surface:          SurfaceWebChat,
		RequestedToolset: "file",
		ExplicitDisables: []string{"write_file"},
		ProfileRoot:      t.TempDir(),
	})

	for _, name := range resolution.ToolNames {
		if name == "write_file" {
			t.Fatal("expected write_file to be removed from enabled tool list")
		}
	}
	if resolution.DisabledTools["write_file"] == "" {
		t.Fatal("expected disabled reason for write_file")
	}
}

func TestRegistryResolveBrowserAvailability(t *testing.T) {
	registry := NewRegistry()
	RegisterCatalogDefaults(registry)

	resolution := registry.Resolve(context.Background(), ResolveRequest{
		Surface:          SurfaceWebAdmin,
		RequestedToolset: "browser",
		ProfileRoot:      t.TempDir(),
		Browser:          testBrowser{err: errors.New("backend offline")},
	})

	if len(resolution.EnabledTools) != 0 {
		t.Fatalf("expected browser tools to be unavailable, got %d enabled", len(resolution.EnabledTools))
	}
	if resolution.Availability["browser_snapshot"] == "" {
		t.Fatal("expected browser availability reason")
	}
}
