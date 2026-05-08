package config

type Config struct {
	Model      ModelConfig                `yaml:"model"`
	Providers  map[string]ProviderConfig  `yaml:"providers"`
	Agent      AgentConfig                `yaml:"agent"`
	Approvals  ApprovalsConfig            `yaml:"approvals"`
	Cron       CronConfig                 `yaml:"cron"`
	Browser    BrowserConfig              `yaml:"browser"`
	Gateway    GatewayConfig              `yaml:"gateway"`
	Web        WebConfig                  `yaml:"web"`
	API        APIConfig                  `yaml:"api"`
	PocketBase PocketBaseConfig           `yaml:"pocketbase"`
	Profiles   ProfilesConfig             `yaml:"profiles"`
	Skills     SkillsConfig               `yaml:"skills"`
	MCPServers map[string]MCPServerConfig `yaml:"mcpServers"`
	Plugins    PluginsConfig              `yaml:"plugins"`
	Auxiliary  map[string]map[string]any  `yaml:"auxiliary"`
}

type ModelConfig struct {
	DefaultProvider string `yaml:"defaultProvider"`
	DefaultModel    string `yaml:"defaultModel"`
}

type ProviderConfig struct {
	BaseURL string            `yaml:"baseURL"`
	Dialect string            `yaml:"dialect"`
	Auth    ProviderAuth      `yaml:"auth"`
	Headers map[string]string `yaml:"headers"`
}

type ProviderAuth struct {
	Env string `yaml:"env"`
}

type AgentConfig struct {
	MaxTurns        int    `yaml:"maxTurns"`
	BusyInputPolicy string `yaml:"busyInputPolicy"`
}

type ApprovalsConfig struct {
	Mode          string   `yaml:"mode"`
	BlockPatterns []string `yaml:"blockPatterns"`
}

type CronConfig struct {
	Enabled      bool   `yaml:"enabled"`
	PollInterval string `yaml:"pollInterval"`
}

type BrowserConfig struct {
	Mode            string   `yaml:"mode"`
	AllowedBackends []string `yaml:"allowedBackends"`
}

type GatewayConfig struct {
	Platforms []string `yaml:"platforms"`
}

type WebConfig struct {
	BindAddress string `yaml:"bindAddress"`
	SessionTTL  string `yaml:"sessionTTL"`
}

type APIConfig struct {
	BearerTokens []string `yaml:"bearerTokens"`
}

type PocketBaseConfig struct {
	DataDir string `yaml:"dataDir"`
}

type ProfilesConfig struct {
	DefaultSlug string `yaml:"defaultSlug"`
}

type SkillsConfig struct {
	RootDir string `yaml:"rootDir"`
}

type MCPServerConfig struct {
	Command string          `yaml:"command"`
	Args    []string        `yaml:"args"`
	Tools   []MCPToolConfig `yaml:"tools"`
}

type MCPToolConfig struct {
	Name              string         `yaml:"name"`
	Description       string         `yaml:"description"`
	InputSchema       map[string]any `yaml:"inputSchema"`
	Toolsets          []string       `yaml:"toolsets"`
	AllowedSurfaces   []string       `yaml:"allowedSurfaces"`
	Interactive       bool           `yaml:"interactive"`
	ApprovalSensitive bool           `yaml:"approvalSensitive"`
	ReadOnly          bool           `yaml:"readOnly"`
	PlatformScoped    bool           `yaml:"platformScoped"`
}

type PluginsConfig struct {
	RepoPaths    []string `yaml:"repoPaths"`
	ProfilePaths []string `yaml:"profilePaths"`
}

func Default() Config {
	return Config{
		Model: ModelConfig{
			DefaultProvider: "ollama-local",
			DefaultModel:    "llama3.2:3b",
		},
		Providers: map[string]ProviderConfig{},
		Agent: AgentConfig{
			MaxTurns:        32,
			BusyInputPolicy: "queue",
		},
		Approvals: ApprovalsConfig{
			Mode:          "manual",
			BlockPatterns: []string{},
		},
		Cron: CronConfig{
			Enabled:      true,
			PollInterval: "60s",
		},
		Browser: BrowserConfig{
			Mode:            "external",
			AllowedBackends: []string{"cdp"},
		},
		Gateway: GatewayConfig{
			Platforms: []string{},
		},
		Web: WebConfig{
			BindAddress: "127.0.0.1:8090",
			SessionTTL:  "24h",
		},
		API: APIConfig{
			BearerTokens: []string{},
		},
		PocketBase: PocketBaseConfig{
			DataDir: "pb_data",
		},
		Profiles: ProfilesConfig{
			DefaultSlug: "default",
		},
		Skills: SkillsConfig{
			RootDir: "skills",
		},
		MCPServers: map[string]MCPServerConfig{},
		Plugins: PluginsConfig{
			RepoPaths:    []string{".agents/plugins"},
			ProfilePaths: []string{"plugins"},
		},
		Auxiliary: map[string]map[string]any{},
	}
}
