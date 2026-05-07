package app

import (
	"context"
	"errors"
	"fmt"
)

// Service defines an explicitly owned long-running component.
type Service interface {
	Name() string
	Start(context.Context) error
	Stop(context.Context) error
}

// Lifecycle manages service startup order and reverse shutdown order.
type Lifecycle struct {
	services []Service
	started  []Service
}

func NewLifecycle(services ...Service) *Lifecycle {
	return &Lifecycle{services: services}
}

func (l *Lifecycle) Add(service Service) {
	l.services = append(l.services, service)
}

func (l *Lifecycle) Start(ctx context.Context) error {
	l.started = l.started[:0]

	for _, service := range l.services {
		if err := service.Start(ctx); err != nil {
			_ = l.stopStarted(ctx)
			return fmt.Errorf("start %s: %w", service.Name(), err)
		}

		l.started = append(l.started, service)
	}

	return nil
}

func (l *Lifecycle) Stop(ctx context.Context) error {
	return l.stopStarted(ctx)
}

func (l *Lifecycle) stopStarted(ctx context.Context) error {
	var joined error

	for i := len(l.started) - 1; i >= 0; i-- {
		service := l.started[i]
		if err := service.Stop(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("stop %s: %w", service.Name(), err))
		}
	}

	l.started = l.started[:0]
	return joined
}
