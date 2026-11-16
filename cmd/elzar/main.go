package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/underwoo/elzar/internal/config"
	"github.com/underwoo/elzar/internal/slurm"
	"github.com/underwoo/elzar/internal/ui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	refresh := flag.String("refresh", "", "queue refresh interval (overrides config)")
	window := flag.String("window", "", "recent jobs window (overrides config)")
	all := flag.Bool("all", false, "show all users by default (overrides config)")
	flag.Parse()

	if *showVersion {
		fmt.Println("elzar", version)
		return
	}

	settings, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "elzar:", err)
		os.Exit(2)
	}
	settings, err = config.Override(settings, *refresh, *window, *all)
	if err != nil {
		fmt.Fprintln(os.Stderr, "elzar:", err)
		os.Exit(2)
	}

	// Resolve the terminal background before Bubble Tea starts reading input.
	lipgloss.HasDarkBackground()

	program := tea.NewProgram(ui.NewWithOptions(slurm.ExecRunner{}, ui.Options{
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
		fmt.Fprintln(os.Stderr, "elzar:", err)
		os.Exit(1)
	}
}
