package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestProfilesEnsureAndList(t *testing.T) {
	opts := Options{
		ProfilesDir: t.TempDir(),
		ProfileSlug: "default",
	}

	var ensureOut bytes.Buffer
	if err := Execute(context.Background(), []string{"profiles", "ensure", "default"}, &ensureOut, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("profiles ensure: %v", err)
	}
	if strings.TrimSpace(ensureOut.String()) == "" {
		t.Fatal("expected ensure to print the profile path")
	}

	var listOut bytes.Buffer
	if err := Execute(context.Background(), []string{"profiles", "list"}, &listOut, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("profiles list: %v", err)
	}
	if !strings.Contains(listOut.String(), "default") {
		t.Fatalf("expected profiles list to include default, got %q", listOut.String())
	}
}
