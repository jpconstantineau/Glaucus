package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase"
)

type RuntimeOptions struct {
	Name string
}

type Runtime struct {
	name       string
	pocketbase *pocketbase.PocketBase
	lifecycle  *Lifecycle
}

func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.Name == "" {
		return nil, errors.New("runtime name is required")
	}

	pb := pocketbase.New()
	runtime := &Runtime{
		name:       opts.Name,
		pocketbase: pb,
		lifecycle:  NewLifecycle(),
	}

	runtime.lifecycle.Add(&pocketbaseService{app: pb})

	return runtime, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if err := r.lifecycle.Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()

	if err := r.lifecycle.Stop(context.Background()); err != nil {
		return fmt.Errorf("shutdown %s: %w", r.name, err)
	}

	return nil
}

type pocketbaseService struct {
	app *pocketbase.PocketBase
}

func (s *pocketbaseService) Name() string {
	return "pocketbase"
}

func (s *pocketbaseService) Start(context.Context) error {
	return nil
}

func (s *pocketbaseService) Stop(context.Context) error {
	event := new(struct{})
	_ = event
	return s.app.ResetBootstrapState()
}
