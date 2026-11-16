package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/underwoo/elzar/internal/config"
	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/slurm"
	"github.com/underwoo/elzar/internal/ui"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "plugins" {
		return runPlugins(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("elzar", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	refresh := fs.String("refresh", "", "queue refresh interval (overrides config)")
	window := fs.String("window", "", "recent jobs window (overrides config)")
	all := fs.Bool("all", false, "show all users by default (overrides config)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "elzar: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "elzar", version)
		return 0
	}

	settings, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "elzar:", err)
		return 2
	}
	settings, err = config.Override(settings, *refresh, *window, *all)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "elzar:", err)
		return 2
	}

	// Resolve the terminal background before Bubble Tea starts reading input.
	lipgloss.HasDarkBackground()

	program := tea.NewProgram(ui.NewWithOptions(slurm.ExecRunner{}, ui.Options{
		Plugins:                settings.Plugins,
		RefreshInterval:        settings.RefreshInterval,
		RecentWindow:           settings.RecentWindow,
		Scope:                  settings.DefaultScope,
		ConfigPath:             settings.Path,
		ConfigNotice:           settings.Notice,
		QueueColumns:           settings.QueueColumns,
		RecentColumns:          settings.RecentColumns,
		TabPrefix:              settings.TabPrefix,
		AccountColumns:         settings.AccountColumns,
		AccountClusters:        settings.AccountClusters,
		AccountRefreshInterval: settings.AccountRefreshInterval,
	}), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		_, _ = fmt.Fprintln(stderr, "elzar:", err)
		return 1
	}
	return 0
}

func runPlugins(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("elzar plugins", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "elzar plugins: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	settings, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "elzar:", err)
		return 2
	}

	diags, hasErrors := plugin.Diagnose(context.Background(), settings.Plugins, nil, ui.ReservedPluginKeys(settings.TabPrefix))
	if err := plugin.FormatDiagnostics(stdout, diags); err != nil {
		_, _ = fmt.Fprintln(stderr, "elzar:", err)
		return 1
	}
	if hasErrors {
		return 1
	}
	return 0
}
