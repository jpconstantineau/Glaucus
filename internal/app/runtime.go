package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/api"
	"github.com/jpconstantineau/Glaucus/internal/approvals"
	"github.com/jpconstantineau/Glaucus/internal/config"
	exportsvc "github.com/jpconstantineau/Glaucus/internal/exports"
	"github.com/jpconstantineau/Glaucus/internal/jobs"
	"github.com/jpconstantineau/Glaucus/internal/memory"
	_ "github.com/jpconstantineau/Glaucus/internal/migrations"
	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	agentruntime "github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/jpconstantineau/Glaucus/internal/search"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/skills"
	"github.com/jpconstantineau/Glaucus/internal/tools"
	"github.com/jpconstantineau/Glaucus/internal/web"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type RuntimeOptions struct {
	Name         string
	ProfilesDir  string
	ProfileSlug  string
	ProvidersDir string
}

type Runtime struct {
	name       string
	pocketbase *pocketbase.PocketBase
	lifecycle  *Lifecycle
	profile    profile.ActiveProfile
	config     config.Loaded
	providers  providers.Catalog
	sessions   *sessions.Service
	jobs       *jobs.Service
	memory     *memory.Service
	search     *search.Service
	skills     *skills.Service
	exports    *exportsvc.Service
	curator    *skills.Curator
	scheduler  *jobs.Scheduler
	events     *agentruntime.EventService
	prompts    *agentruntime.PromptBuilder
	router     *providers.Router
	runs       *agentruntime.Orchestrator
	tools      *tools.Registry
	web        *web.Module
	server     *pocketbaseService
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
	if opts.ProvidersDir == "" {
		opts.ProvidersDir = filepath.Join("providers", "manifests")
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

	catalog, err := providers.LoadCatalog(opts.ProvidersDir)
	if err != nil {
		return nil, fmt.Errorf("load provider catalog: %w", err)
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
		providers:  catalog,
	}
	runtime.sessions = sessions.NewService(pb)
	runtime.jobs = jobs.NewService(pb)
	runtime.memory = memory.NewService(pb)
	runtime.events = agentruntime.NewEventService(pb)
	runtime.prompts = agentruntime.NewPromptBuilder()
	runtime.search = search.NewService(pb, runtime.sessions)
	runtime.skills = skills.NewService(pb)
	runtime.exports = exportsvc.NewService(pb)
	runtime.router = providers.NewRouter(catalog, loadedConfig.Config)
	runtime.tools = tools.NewRegistry()
	tools.RegisterCatalogDefaults(runtime.tools)
	tools.RegisterFileTools(runtime.tools)
	processService := tools.NewBackgroundProcessService(pb)
	tools.RegisterProcessTools(runtime.tools, processService)
	tools.RegisterWebTools(runtime.tools, tools.NewHTTPWebBackend(), nil)
	tools.RegisterJobTools(runtime.tools, jobToolAdapter{service: runtime.jobs})
	tools.RegisterPlanningTools(runtime.tools, todoToolAdapter{service: runtime.sessions}, memoryToolAdapter{service: runtime.memory, profileRoot: activeProfile.Root}, searchToolAdapter{service: runtime.search})
	tools.RegisterSkillsTools(runtime.tools, skillsToolAdapter{service: runtime.skills, profileRoot: activeProfile.Root})
	approvalService := approvals.NewService(pb, loadedConfig.Config.Approvals)
	runtime.runs = agentruntime.NewOrchestrator(runtime.sessions, runtime.router, runtime.events, runtime.tools, approvalService)
	pollInterval, err := time.ParseDuration(loadedConfig.Config.Cron.PollInterval)
	if err != nil || pollInterval <= 0 {
		pollInterval = time.Minute
	}
	runtime.scheduler = jobs.NewScheduler(activeProfile.Slug, loadedConfig.Config.Cron.Enabled, pollInterval, runtime.jobs, runtime.sessions, jobs.RuntimeExecutor{
		Profile:       activeProfile,
		Config:        loadedConfig.Config,
		Sessions:      runtime.sessions,
		PromptBuilder: runtime.prompts,
		Orchestrator:  runtime.runs,
		ToolRegistry:  runtime.tools,
	}, runtime.events)
	runtime.curator = skills.NewCurator(runtime.skills, 6*time.Hour)

	sessionTTL, err := time.ParseDuration(loadedConfig.Config.Web.SessionTTL)
	if err != nil || sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}

	runtime.web = web.Register(pb, web.Options{
		AppName:                 opts.Name,
		Version:                 "dev",
		Commit:                  "local",
		BuiltAt:                 "unknown",
		BindAddress:             loadedConfig.Config.Web.BindAddress,
		SessionTTL:              sessionTTL,
		Profile:                 activeProfile,
		ProviderCatalog:         catalog,
		SessionService:          runtime.sessions,
		JobService:              runtime.jobs,
		SearchService:           runtime.search,
		SkillsService:           runtime.skills,
		ExportService:           runtime.exports,
		Scheduler:               runtime.scheduler,
		EventService:            runtime.events,
		PromptBuilder:           runtime.prompts,
		Orchestrator:            runtime.runs,
		ApprovalService:         approvalService,
		ToolRegistry:            runtime.tools,
		LoadedConfig:            loadedConfig.Config,
		DefaultOperatorEmail:    "admin@glaucus.local",
		DefaultOperatorPassword: "glaucus-admin",
	})
	api.Register(pb, api.Options{
		Profile:         activeProfile,
		Config:          loadedConfig.Config,
		ProviderCatalog: catalog,
		Router:          runtime.router,
		SessionService:  runtime.sessions,
		JobService:      runtime.jobs,
		EventService:    runtime.events,
		PromptBuilder:   runtime.prompts,
		ToolRegistry:    runtime.tools,
		Orchestrator:    runtime.runs,
	})

	runtime.server = &pocketbaseService{
		app:              pb,
		bindAddress:      loadedConfig.Config.Web.BindAddress,
		operatorEmail:    "admin@glaucus.local",
		operatorPassword: "glaucus-admin",
		serveDone:        make(chan error, 1),
	}
	runtime.lifecycle.Add(runtime.server)
	runtime.lifecycle.Add(runtime.scheduler)
	runtime.lifecycle.Add(runtime.curator)

	return runtime, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if err := r.lifecycle.Start(ctx); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case err := <-r.server.serveDone:
		if err != nil {
			return err
		}
	}

	if err := r.lifecycle.Stop(context.Background()); err != nil {
		return fmt.Errorf("shutdown %s: %w", r.name, err)
	}

	return nil
}

type pocketbaseService struct {
	app              *pocketbase.PocketBase
	bindAddress      string
	operatorEmail    string
	operatorPassword string
	serveDone        chan error
}

func (s *pocketbaseService) Name() string {
	return "pocketbase"
}

func (s *pocketbaseService) Start(context.Context) error {
	if err := s.app.Bootstrap(); err != nil {
		return err
	}
	if err := s.app.RunAllMigrations(); err != nil {
		return err
	}
	if err := web.EnsureDefaultOperator(s.app, s.operatorEmail, s.operatorPassword); err != nil {
		return err
	}

	go func() {
		s.serveDone <- apis.Serve(s.app, apis.ServeConfig{
			HttpAddr:        s.bindAddress,
			ShowStartBanner: false,
		})
	}()

	return nil
}

func (s *pocketbaseService) Stop(context.Context) error {
	event := &core.TerminateEvent{App: s.app}
	if err := s.app.OnTerminate().Trigger(event, func(e *core.TerminateEvent) error {
		return e.App.ResetBootstrapState()
	}); err != nil {
		return err
	}

	select {
	case err := <-s.serveDone:
		return err
	default:
		return nil
	}
}
