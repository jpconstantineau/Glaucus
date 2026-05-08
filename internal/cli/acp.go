package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jpconstantineau/Glaucus/internal/app"
)

const acpVersion = "2026-05-01"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func acpCommand(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options) error {
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(stdin)
	for {
		body, err := readRPCFrame(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			if writeErr := writeRPCFrame(stdout, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}); writeErr != nil {
				return writeErr
			}
			continue
		}

		response := handleACPRequest(ctx, rt, req)
		if err := writeRPCFrame(stdout, response); err != nil {
			return err
		}
		if req.Method == "shutdown" {
			return nil
		}
	}
}

func handleACPRequest(ctx context.Context, rt *app.Runtime, req rpcRequest) rpcResponse {
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		response.Result = map[string]any{
			"name":    "Glaucus ACP",
			"version": acpVersion,
			"methods": []string{"runs/create", "runs/get", "runs/events", "shutdown"},
		}
	case "runs/create":
		var params struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.Prompt) == "" {
			response.Error = &rpcError{Code: -32602, Message: "prompt is required"}
			return response
		}
		run, output, err := rt.ExecutePromptRun(ctx, params.Prompt)
		if err != nil {
			response.Error = &rpcError{Code: -32000, Message: err.Error()}
			return response
		}
		response.Result = map[string]any{
			"run_id":     run.ID,
			"session_id": run.SessionID,
			"status":     run.Status,
			"output":     output,
		}
	case "runs/get":
		var params struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.RunID) == "" {
			response.Error = &rpcError{Code: -32602, Message: "run_id is required"}
			return response
		}
		run, err := rt.GetRun(ctx, params.RunID)
		if err != nil {
			response.Error = &rpcError{Code: -32001, Message: err.Error()}
			return response
		}
		response.Result = map[string]any{
			"id":         run.ID,
			"session_id": run.SessionID,
			"status":     run.Status,
			"started_at": run.StartedAt,
			"ended_at":   run.EndedAt,
		}
	case "runs/events":
		var params struct {
			RunID string `json:"run_id"`
			After int    `json:"after"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || strings.TrimSpace(params.RunID) == "" {
			response.Error = &rpcError{Code: -32602, Message: "run_id is required"}
			return response
		}
		events, err := rt.ListRunEvents(ctx, params.RunID, params.After)
		if err != nil {
			response.Error = &rpcError{Code: -32001, Message: err.Error()}
			return response
		}
		response.Result = map[string]any{"events": events}
	case "shutdown":
		response.Result = map[string]any{"ok": true}
	default:
		response.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return response
}

func readRPCFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			value = strings.TrimSpace(strings.TrimPrefix(value, "content-length:"))
			contentLength, err = strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength <= 0 {
		return nil, io.EOF
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeRPCFrame(writer io.Writer, response rpcResponse) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = writer.Write(body)
	return err
}
