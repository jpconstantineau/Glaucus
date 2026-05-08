package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/Glaucus/internal/app"
	"github.com/jpconstantineau/Glaucus/internal/profile"
)

type Options struct {
	Name         string
	ProfilesDir  string
	ProfileSlug  string
	ProvidersDir string
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, opts Options) error {
	if opts.Name == "" {
		opts.Name = "Glaucus"
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

	command := "serve"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		command = args[0]
	}

	switch command {
	case "serve":
		rt, err := newRuntime(opts)
		if err != nil {
			return err
		}
		return rt.Run(ctx)
	case "profiles":
		return profilesCommand(args[1:], stdout, opts)
	case "doctor":
		return doctorCommand(ctx, stdout, opts)
	case "migrate":
		return migrateCommand(stdout, opts)
	case "export":
		return exportCommand(ctx, stdout, opts)
	case "import":
		return importCommand(stdout, args[1:], opts)
	case "prompt":
		return promptCommand(ctx, stdout, args[1:], opts)
	case "acp":
		return acpCommand(ctx, os.Stdin, stdout, opts)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", command)
		return errors.New("unknown command")
	}
}

func profilesCommand(args []string, stdout io.Writer, opts Options) error {
	subcommand := "list"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		subcommand = args[0]
	}
	switch subcommand {
	case "list":
		entries, err := os.ReadDir(opts.ProfilesDir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			marker := ""
			if entry.Name() == opts.ProfileSlug {
				marker = " *"
			}
			fmt.Fprintf(stdout, "%s%s\n", entry.Name(), marker)
		}
		return nil
	case "ensure":
		if len(args) < 2 {
			return errors.New("profiles ensure requires a slug")
		}
		active, err := profile.Bootstrap(profile.BootstrapOptions{BaseDir: opts.ProfilesDir, Slug: args[1]})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, active.Root)
		return nil
	default:
		return fmt.Errorf("unknown profiles subcommand %q", subcommand)
	}
}

func doctorCommand(ctx context.Context, stdout io.Writer, opts Options) error {
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}
	report, err := rt.Doctor(ctx)
	if err != nil {
		return err
	}
	body, _ := json.MarshalIndent(report, "", "  ")
	fmt.Fprintln(stdout, string(body))
	return nil
}

func migrateCommand(stdout io.Writer, opts Options) error {
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}
	if err := rt.PrepareStorage(); err != nil {
		return err
	}
	defer rt.CloseStorage()
	fmt.Fprintln(stdout, "migrations applied")
	return nil
}

func exportCommand(ctx context.Context, stdout io.Writer, opts Options) error {
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}
	record, err := rt.CreateProfileExport(ctx, "cli")
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, record.Path)
	return nil
}

func importCommand(stdout io.Writer, args []string, opts Options) error {
	if len(args) == 0 {
		return errors.New("import requires a package path")
	}
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}
	validation, err := rt.ValidateImportPackage(args[0])
	if err != nil {
		return err
	}
	body, _ := json.MarshalIndent(validation, "", "  ")
	fmt.Fprintln(stdout, string(body))
	return nil
}

func promptCommand(ctx context.Context, stdout io.Writer, args []string, opts Options) error {
	if len(args) == 0 {
		return errors.New("prompt requires input text")
	}
	rt, err := newRuntime(opts)
	if err != nil {
		return err
	}
	output, err := rt.ExecutePrompt(ctx, strings.Join(args, " "))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, output)
	return nil
}

func newRuntime(opts Options) (*app.Runtime, error) {
	return app.NewRuntime(app.RuntimeOptions{
		Name:         opts.Name,
		ProfilesDir:  opts.ProfilesDir,
		ProfileSlug:  opts.ProfileSlug,
		ProvidersDir: opts.ProvidersDir,
	})
}
