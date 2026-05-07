package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const CollectionBackgroundProcesses = "background_processes"

type BackgroundProcess struct {
	ID        string
	ProfileID string
	SessionID string
	RunID     string
	Handle    string
	Command   string
	CWD       string
	Status    string
	StartedAt time.Time
	EndedAt   time.Time
	ExitCode  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type StartProcessInput struct {
	ProfileID string
	SessionID string
	RunID     string
	Command   string
	CWD       string
}

type liveProcess struct {
	cmd        *exec.Cmd
	stdoutPath string
	stderrPath string
}

type BackgroundProcessService struct {
	app  core.App
	mu   sync.RWMutex
	live map[string]liveProcess
}

func NewBackgroundProcessService(app core.App) *BackgroundProcessService {
	return &BackgroundProcessService{
		app:  app,
		live: map[string]liveProcess{},
	}
}

func (s *BackgroundProcessService) Start(ctx context.Context, input StartProcessInput) (BackgroundProcess, map[string]any, error) {
	handle, err := randomHandle()
	if err != nil {
		return BackgroundProcess{}, nil, err
	}

	record, err := s.newRecord()
	if err != nil {
		return BackgroundProcess{}, nil, err
	}
	record.Set("profile_id", input.ProfileID)
	record.Set("session_id", input.SessionID)
	record.Set("run_id", input.RunID)
	record.Set("handle", handle)
	record.Set("command", input.Command)
	record.Set("cwd", input.CWD)
	record.Set("status", "starting")
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return BackgroundProcess{}, nil, fmt.Errorf("save background process: %w", err)
	}

	logDir := input.CWD
	if strings.TrimSpace(logDir) == "" {
		logDir = os.TempDir()
	}
	logDir = filepathJoin(logDir, ".glaucus-process-logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return BackgroundProcess{}, nil, fmt.Errorf("create process log dir: %w", err)
	}
	stdoutPath := filepathJoin(logDir, handle+".stdout.log")
	stderrPath := filepathJoin(logDir, handle+".stderr.log")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return BackgroundProcess{}, nil, fmt.Errorf("create stdout log: %w", err)
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return BackgroundProcess{}, nil, fmt.Errorf("create stderr log: %w", err)
	}

	cmd := shellCommandContext(context.Background(), input.Command)
	cmd.Dir = input.CWD
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		_, _ = s.update(record.Id, "failed", time.Now().UTC(), -1)
		return BackgroundProcess{}, nil, fmt.Errorf("start process: %w", err)
	}

	startedAt := time.Now().UTC()
	process, err := s.update(record.Id, "running", startedAt, 0)
	if err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		return BackgroundProcess{}, nil, err
	}

	s.mu.Lock()
	s.live[handle] = liveProcess{
		cmd:        cmd,
		stdoutPath: stdoutPath,
		stderrPath: stderrPath,
	}
	s.mu.Unlock()

	go func(recordID string, handle string) {
		err := cmd.Wait()
		_ = stdoutFile.Close()
		_ = stderrFile.Close()

		status := "completed"
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		if err != nil {
			status = "failed"
			if exitCode == 0 {
				exitCode = 1
			}
		}
		_, _ = s.update(recordID, status, time.Now().UTC(), exitCode)

		s.mu.Lock()
		delete(s.live, handle)
		s.mu.Unlock()
	}(record.Id, handle)

	return process, map[string]any{
		"stdout_path": stdoutPath,
		"stderr_path": stderrPath,
		"pid":         cmd.Process.Pid,
	}, nil
}

func (s *BackgroundProcessService) Inspect(ctx context.Context, handle string) (BackgroundProcess, map[string]any, error) {
	record, err := s.findByHandle(handle)
	if err != nil {
		return BackgroundProcess{}, nil, err
	}
	process, err := backgroundProcessFromRecord(record)
	if err != nil {
		return BackgroundProcess{}, nil, err
	}

	s.mu.RLock()
	live, ok := s.live[handle]
	s.mu.RUnlock()

	details := map[string]any{}
	if ok {
		details["stdout_tail"] = readTail(live.stdoutPath, 4096)
		details["stderr_tail"] = readTail(live.stderrPath, 4096)
		if live.cmd.Process != nil {
			details["pid"] = live.cmd.Process.Pid
		}
	}

	_ = ctx
	return process, details, nil
}

func (s *BackgroundProcessService) Stop(ctx context.Context, handle string) (BackgroundProcess, error) {
	record, err := s.findByHandle(handle)
	if err != nil {
		return BackgroundProcess{}, err
	}

	s.mu.RLock()
	live, ok := s.live[handle]
	s.mu.RUnlock()
	if ok && live.cmd.Process != nil {
		if err := live.cmd.Process.Kill(); err != nil {
			return BackgroundProcess{}, fmt.Errorf("stop process: %w", err)
		}
	}

	process, err := s.update(record.Id, "stopped", time.Now().UTC(), -1)
	if err != nil {
		return BackgroundProcess{}, err
	}
	_ = ctx
	return process, nil
}

func (s *BackgroundProcessService) findByHandle(handle string) (*core.Record, error) {
	record, err := s.app.FindFirstRecordByFilter(
		CollectionBackgroundProcesses,
		"handle = {:handle}",
		dbx.Params{"handle": handle},
	)
	if err != nil {
		return nil, fmt.Errorf("find process by handle: %w", err)
	}
	return record, nil
}

func (s *BackgroundProcessService) update(id, status string, endedAt time.Time, exitCode int) (BackgroundProcess, error) {
	record, err := s.app.FindRecordById(CollectionBackgroundProcesses, id)
	if err != nil {
		return BackgroundProcess{}, fmt.Errorf("find process record: %w", err)
	}

	record.Set("status", status)
	if status == "running" {
		if dt, err := types.ParseDateTime(endedAt); err == nil {
			record.Set("started_at", dt)
		}
	} else {
		if dt, err := types.ParseDateTime(endedAt); err == nil {
			record.Set("ended_at", dt)
		}
	}
	record.Set("exit_code", exitCode)
	if err := s.app.Save(record); err != nil {
		return BackgroundProcess{}, fmt.Errorf("save process record: %w", err)
	}
	return backgroundProcessFromRecord(record)
}

func (s *BackgroundProcessService) newRecord() (*core.Record, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollectionBackgroundProcesses)
	if err != nil {
		return nil, fmt.Errorf("find background process collection: %w", err)
	}
	return core.NewRecord(collection), nil
}

func backgroundProcessFromRecord(record *core.Record) (BackgroundProcess, error) {
	return BackgroundProcess{
		ID:        record.Id,
		ProfileID: record.GetString("profile_id"),
		SessionID: record.GetString("session_id"),
		RunID:     record.GetString("run_id"),
		Handle:    record.GetString("handle"),
		Command:   record.GetString("command"),
		CWD:       record.GetString("cwd"),
		Status:    record.GetString("status"),
		StartedAt: record.GetDateTime("started_at").Time(),
		EndedAt:   record.GetDateTime("ended_at").Time(),
		ExitCode:  record.GetInt("exit_code"),
		CreatedAt: record.GetDateTime("created").Time(),
		UpdatedAt: record.GetDateTime("updated").Time(),
	}, nil
}

func shellCommandContext(ctx context.Context, command string) *exec.Cmd {
	if goruntime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoLogo", "-NoProfile", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-lc", command)
}

func randomHandle() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func readTail(path string, maxBytes int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return string(data)
}

func filepathJoin(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}
