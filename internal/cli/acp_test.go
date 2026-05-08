package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRPCFrameRoundTrip(t *testing.T) {
	var out bytes.Buffer
	if err := writeRPCFrame(&out, rpcResponse{JSONRPC: "2.0", ID: 1, Result: map[string]any{"ok": true}}); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	body, err := readRPCFrame(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	var response rpcResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.JSONRPC != "2.0" {
		t.Fatalf("unexpected rpc version: %+v", response)
	}
}
