package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type FlagOverrides struct {
	ModelDefaultProvider string
	ModelDefaultModel    string
	WebBindAddress       string
	PocketBaseDataDir    string
}

type Loaded struct {
	Config     Config
	ConfigPath string
	EnvPath    string
}

func Load(profileRoot string, flags FlagOverrides) (Loaded, error) {
	configPath := filepath.Join(profileRoot, "config.yaml")
	envPath := filepath.Join(profileRoot, ".env")

	cfg := Default()

	envValues, err := parseDotEnvFile(envPath)
	if err != nil {
		return Loaded{}, err
	}
	applyEnv(&cfg, envValues)

	if err := applyYAMLFile(configPath, &cfg); err != nil {
		return Loaded{}, err
	}

	applyFlags(&cfg, flags)

	if err := validate(cfg); err != nil {
		return Loaded{}, err
	}

	return Loaded{
		Config:     cfg,
		ConfigPath: configPath,
		EnvPath:    envPath,
	}, nil
}

func applyYAMLFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	return nil
}

func parseDotEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read env file: %w", err)
	}

	values := map[string]string{}

	for lineNumber, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("parse env file %s line %d: expected KEY=VALUE", path, lineNumber+1)
		}

		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	return values, nil
}

func applyEnv(cfg *Config, values map[string]string) {
	if v := values["GLAUCUS_MODEL_DEFAULT_PROVIDER"]; v != "" {
		cfg.Model.DefaultProvider = v
	}
	if v := values["GLAUCUS_MODEL_DEFAULT_MODEL"]; v != "" {
		cfg.Model.DefaultModel = v
	}
	if v := values["GLAUCUS_WEB_BIND_ADDRESS"]; v != "" {
		cfg.Web.BindAddress = v
	}
	if v := values["GLAUCUS_POCKETBASE_DATA_DIR"]; v != "" {
		cfg.PocketBase.DataDir = v
	}
}

func applyFlags(cfg *Config, flags FlagOverrides) {
	if flags.ModelDefaultProvider != "" {
		cfg.Model.DefaultProvider = flags.ModelDefaultProvider
	}
	if flags.ModelDefaultModel != "" {
		cfg.Model.DefaultModel = flags.ModelDefaultModel
	}
	if flags.WebBindAddress != "" {
		cfg.Web.BindAddress = flags.WebBindAddress
	}
	if flags.PocketBaseDataDir != "" {
		cfg.PocketBase.DataDir = flags.PocketBaseDataDir
	}
}

func validate(cfg Config) error {
	if cfg.Model.DefaultProvider == "" {
		return errors.New("config validation failed: model.defaultProvider is required")
	}
	if cfg.Model.DefaultModel == "" {
		return errors.New("config validation failed: model.defaultModel is required")
	}
	if cfg.Web.BindAddress == "" {
		return errors.New("config validation failed: web.bindAddress is required")
	}
	if cfg.PocketBase.DataDir == "" {
		return errors.New("config validation failed: pocketbase.dataDir is required")
	}

	return nil
}
