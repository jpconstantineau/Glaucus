package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentruntime "github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
)

type SchedulerStatus struct {
	Enabled         bool      `json:"enabled"`
	PollInterval    string    `json:"poll_interval"`
	LastTickAt      time.Time `json:"last_tick_at"`
	LastSuccessAt   time.Time `json:"last_success_at"`
	LastError       string    `json:"last_error,omitempty"`
	RecoveredRuns   int       `json:"recovered_runs"`
	RecoveredJobs   int       `json:"recovered_jobs"`
	DispatchedJobs  int       `json:"dispatched_jobs"`
	LastDispatchJob string    `json:"last_dispatch_job,omitempty"`
}

type Scheduler struct {
	profileID string
	enabled   bool
	interval  time.Duration
	jobs      *Service
	sessions  *sessions.Service
	executor  Executor
	events    *agentruntime.EventService
	now       func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.RWMutex
	status SchedulerStatus
}

func NewScheduler(profileID string, enabled bool, interval time.Duration, jobsService *Service, sessionService *sessions.Service, executor Executor, eventService *agentruntime.EventService) *Scheduler {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Scheduler{
		profileID: profileID,
		enabled:   enabled,
		interval:  interval,
		jobs:      jobsService,
		sessions:  sessionService,
		executor:  executor,
		events:    eventService,
		now:       func() time.Time { return time.Now().UTC() },
		status: SchedulerStatus{
			Enabled:      enabled,
			PollInterval: interval.String(),
		},
	}
}

func (s *Scheduler) Name() string {
	return "scheduler"
}

func (s *Scheduler) Start(ctx context.Context) error {
	if s.jobs == nil || s.sessions == nil || s.executor == nil {
		return nil
	}
	if err := s.reconcile(ctx); err != nil {
		s.setError(err)
	}
	if !s.enabled {
		return nil
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop(loopCtx)
	}()
	return nil
}

func (s *Scheduler) Stop(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Scheduler) Status() SchedulerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	s.status.LastTickAt = now
	s.mu.Unlock()

	dueJobs, err := s.jobs.ListDueJobs(ctx, s.profileID, now, 20)
	if err != nil {
		s.setError(err)
		return
	}

	for _, job := range dueJobs {
		if err := s.dispatchJob(ctx, job, now); err != nil {
			s.setError(err)
		}
	}

	s.mu.Lock()
	s.status.LastSuccessAt = now
	s.status.LastError = ""
	s.mu.Unlock()
}

func (s *Scheduler) reconcile(ctx context.Context) error {
	var joined error
	activeRuns, err := s.sessions.ListActiveRuns(ctx, s.profileID, 100)
	if err != nil {
		joined = errorsJoin(joined, err)
	} else {
		recovered := 0
		for _, run := range activeRuns {
			status := "failed"
			message := "reconciled after scheduler restart"
			if run.Status == "running" {
				status = "cancelled"
				message = "cancelled after scheduler restart"
			}
			if _, err := s.sessions.UpdateRun(context.Background(), run.ID, sessions.UpdateRunInput{
				Status:       status,
				EndedAt:      s.now(),
				ErrorCode:    "reconciled_restart",
				ErrorMessage: message,
			}); err != nil {
				joined = errorsJoin(joined, err)
				continue
			}
			recovered++
		}
		s.mu.Lock()
		s.status.RecoveredRuns += recovered
		s.mu.Unlock()
	}

	activeJobRuns, err := s.jobs.ListActiveJobRuns(ctx, s.profileID, 100)
	if err != nil {
		joined = errorsJoin(joined, err)
	} else {
		recovered := 0
		for _, jobRun := range activeJobRuns {
			if _, err := s.jobs.UpdateRun(context.Background(), jobRun.ID, UpdateRunInput{
				Status:       JobStatusFailed,
				EndedAt:      s.now(),
				ErrorMessage: "reconciled after scheduler restart",
			}); err != nil {
				joined = errorsJoin(joined, err)
				continue
			}
			recovered++
		}
		s.mu.Lock()
		s.status.RecoveredJobs += recovered
		s.mu.Unlock()
	}

	return joined
}

func (s *Scheduler) dispatchJob(ctx context.Context, job Job, scheduledFor time.Time) error {
	jobRun, err := s.jobs.RecordRun(ctx, RecordRunInput{
		ProfileID:    job.ProfileID,
		JobID:        job.ID,
		Status:       JobStatusRunning,
		ScheduledFor: scheduledFor,
		StartedAt:    scheduledFor,
	})
	if err != nil {
		return err
	}

	result, execErr := s.executor.ExecuteJob(ctx, job)
	status := JobStatusCompleted
	errorMessage := ""
	output := result.OutputText
	if execErr != nil {
		status = JobStatusFailed
		errorMessage = execErr.Error()
	}

	if _, err := s.jobs.UpdateRun(context.Background(), jobRun.ID, UpdateRunInput{
		RunID:         result.RunID,
		Status:        status,
		EndedAt:       s.now(),
		OutputExcerpt: output,
		ErrorMessage:  errorMessage,
	}); err != nil {
		return err
	}
	if _, err := s.jobs.MarkJobTriggered(context.Background(), job.ID, scheduledFor); err != nil {
		return err
	}

	s.mu.Lock()
	s.status.DispatchedJobs++
	s.status.LastDispatchJob = job.ID
	s.mu.Unlock()
	if execErr != nil {
		return fmt.Errorf("dispatch job %s: %w", job.ID, execErr)
	}
	return nil
}

func (s *Scheduler) setError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastError = err.Error()
}

func errorsJoin(base error, next error) error {
	if base == nil {
		return next
	}
	if next == nil {
		return base
	}
	return fmt.Errorf("%v; %w", base, next)
}
