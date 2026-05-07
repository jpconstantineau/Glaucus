package skills

import (
	"context"
	"sync"
	"time"
)

type Curator struct {
	service  *Service
	interval time.Duration
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewCurator(service *Service, interval time.Duration) *Curator {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	return &Curator{service: service, interval: interval}
}

func (c *Curator) Name() string {
	return "skills-curator"
}

func (c *Curator) Start(ctx context.Context) error {
	_ = ctx
	if c.service == nil {
		return nil
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		_, _ = c.service.ReconcileLifecycle(context.Background())
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				_, _ = c.service.ReconcileLifecycle(context.Background())
			}
		}
	}()
	return nil
}

func (c *Curator) Stop(ctx context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.wg.Wait()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
