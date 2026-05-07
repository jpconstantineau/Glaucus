package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jpconstantineau/Glaucus/internal/config"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/pocketbase"
)

type RuntimeOptions struct {
	Name        string
	ProfilesDir string
	ProfileSlug string
}

type Runtime struct {
	name       string
	pocketbase *pocketbase.PocketBase
	lifecycle  *Lifecycle
	profile    profile.ActiveProfile
	config     config.Loaded
}

func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.Name == "" {
		return nil, errors.New("runtime name is required")
	}
	if opts.ProfilesDir == "" {
		opts.ProfilesDir = "profiles"
	}
	if opts.ProfileSlug == "" {
		opts.ProfileSlug = "default"
	}

	activeProfile, err := profile.Bootstrap(profile.BootstrapOptions{
		BaseDir: opts.ProfilesDir,
		Slug:    opts.ProfileSlug,
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap profile: %w", err)
	}

	loadedConfig, err := config.Load(activeProfile.Root, config.FlagOverrides{})
	if err != nil {
		return nil, fmt.Errorf("load profile config: %w", err)
	}

	dataDir := loadedConfig.Config.PocketBase.DataDir
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(activeProfile.Root, dataDir)
	}

	pb := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  dataDir,
		HideStartBanner: true,
	})
	runtime := &Runtime{
		name:       opts.Name,
		pocketbase: pb,
		lifecycle:  NewLifecycle(),
		profile:    activeProfile,
		config:     loadedConfig,
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
