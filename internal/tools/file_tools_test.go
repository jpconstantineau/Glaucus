package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileToolReadsLineWindow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result := ReadFileTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"path":       "note.txt",
			"start_line": 2,
			"limit":      2,
		},
	})

	if result.Status != StatusSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}
	if !strings.Contains(result.DisplayText, "two\nthree") {
		t.Fatalf("expected requested line window, got %q", result.DisplayText)
	}
}

func TestWriteAndPatchToolsStayWithinApprovedRoots(t *testing.T) {
	root := t.TempDir()

	writeResult := WriteFileTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"path":    "docs/test.txt",
			"content": "alpha\nbeta\n",
		},
	})
	if writeResult.Status != StatusSuccess {
		t.Fatalf("expected write success, got %s", writeResult.Status)
	}

	patchResult := PatchTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"path":        "docs/test.txt",
			"start_line":  2,
			"end_line":    2,
			"replacement": "gamma",
		},
	})
	if patchResult.Status != StatusSuccess {
		t.Fatalf("expected patch success, got %s", patchResult.Status)
	}

	data, err := os.ReadFile(filepath.Join(root, "docs", "test.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if !strings.Contains(string(data), "gamma") {
		t.Fatalf("expected patched content, got %q", string(data))
	}

	escapeResult := WriteFileTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"path":    "..\\escape.txt",
			"content": "nope",
		},
	})
	if escapeResult.Status == StatusSuccess {
		t.Fatal("expected escape path to be rejected")
	}
}

func TestSearchFilesToolSkipsBinaryContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "match.txt"), []byte("hello tool\n"), 0o644); err != nil {
		t.Fatalf("write text fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	result := SearchFilesTool{}.Execute(context.Background(), ToolRequest{
		ProfileRoot:      root,
		WorkingDirectory: root,
		Arguments: map[string]any{
			"query": "tool",
		},
	})

	if result.Status != StatusSuccess {
		t.Fatalf("expected search success, got %s", result.Status)
	}
	if !strings.Contains(result.DisplayText, "match.txt") {
		t.Fatalf("expected search match in text file, got %q", result.DisplayText)
	}
	if strings.Contains(result.DisplayText, "binary.bin") {
		t.Fatalf("expected binary file to be skipped, got %q", result.DisplayText)
	}
}
