package app

import (
	"context"

	"github.com/jpconstantineau/Glaucus/internal/jobs"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type jobToolAdapter struct {
	service *jobs.Service
}

func (a jobToolAdapter) ListJobs(ctx context.Context, profileID string, limit int) (any, error) {
	return a.service.ListJobs(ctx, profileID, limit)
}

func (a jobToolAdapter) GetJob(ctx context.Context, jobID string) (any, error) {
	return a.service.GetJob(ctx, jobID)
}

func (a jobToolAdapter) CreateJob(ctx context.Context, input tools.CronJobCreateInput) (any, error) {
	return a.service.CreateJob(ctx, jobs.CreateJobInput{
		ProfileID:         input.ProfileID,
		Name:              input.Name,
		Prompt:            input.Prompt,
		ScheduleKind:      input.ScheduleKind,
		ScheduleValue:     input.ScheduleValue,
		Timezone:          input.Timezone,
		Enabled:           input.Enabled,
		DeliveryTarget:    input.DeliveryTarget,
		ToolsetOverrides:  input.ToolsetOverrides,
		ProviderOverrides: input.ProviderOverrides,
		CWD:               input.CWD,
	})
}

func (a jobToolAdapter) UpdateJob(ctx context.Context, jobID string, input tools.CronJobUpdateInput) (any, error) {
	return a.service.UpdateJob(ctx, jobID, jobs.UpdateJobInput{
		Name:              input.Name,
		Prompt:            input.Prompt,
		ScheduleKind:      input.ScheduleKind,
		ScheduleValue:     input.ScheduleValue,
		Timezone:          input.Timezone,
		Enabled:           input.Enabled,
		DeliveryTarget:    input.DeliveryTarget,
		ToolsetOverrides:  input.ToolsetOverrides,
		ProviderOverrides: input.ProviderOverrides,
		CWD:               input.CWD,
	})
}

func (a jobToolAdapter) PauseJob(ctx context.Context, jobID string) (any, error) {
	return a.service.PauseJob(ctx, jobID)
}

func (a jobToolAdapter) ResumeJob(ctx context.Context, jobID string) (any, error) {
	return a.service.ResumeJob(ctx, jobID)
}

func (a jobToolAdapter) DeleteJob(ctx context.Context, jobID string) error {
	return a.service.DeleteJob(ctx, jobID)
}

func (a jobToolAdapter) QueueManualRun(ctx context.Context, profileID string, jobID string) (any, any, error) {
	job, err := a.service.GetJob(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	jobRun, err := a.service.RecordRun(ctx, jobs.RecordRunInput{
		ProfileID:    profileID,
		JobID:        jobID,
		Status:       jobs.JobStatusQueued,
		ScheduledFor: a.service.Now(),
	})
	if err != nil {
		return nil, nil, err
	}
	return job, jobRun, nil
}

func (a jobToolAdapter) ListJobRuns(ctx context.Context, jobID string, limit int) (any, error) {
	return a.service.ListRuns(ctx, jobID, limit)
}
