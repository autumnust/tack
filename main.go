package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/standup-kanban/standup-kanban/internal/github"
	"github.com/standup-kanban/standup-kanban/internal/model"
	"github.com/standup-kanban/standup-kanban/internal/tui"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	client, err := github.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(config, *configPath, client)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var config model.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return model.Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if config.Project == "" {
		return model.Config{}, fmt.Errorf("'project' is required in config")
	}
	if config.StatusField == "" {
		config.StatusField = "Status"
	}
	// Focus is required — either global or per-person
	hasFocus := len(config.Focus) > 0
	if !hasFocus {
		for _, t := range config.Team {
			if len(t.Focus) > 0 {
				hasFocus = true
				break
			}
		}
	}
	if !hasFocus {
		return model.Config{}, fmt.Errorf("'focus' is required — add global focus issue numbers or per-team-member focus lists")
	}
	return config, nil
}
