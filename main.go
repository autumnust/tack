package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/autumnust/tack/internal/model"
	"github.com/autumnust/tack/internal/planning"
	"github.com/autumnust/tack/internal/tui"

	ghclient "github.com/autumnust/tack/internal/github"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	planMode := flag.Bool("plan", false, "start in planning mode")
	statsMode := flag.Bool("stats", false, "print command usage stats and exit")
	recapMode := flag.Bool("recap", false, "generate weekly recap and exit (for cron)")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	// Non-interactive modes
	if *statsMode {
		printStats(config)
		return
	}
	if *recapMode {
		generateRecap(config)
		return
	}

	client, err := ghclient.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(config, *configPath, client, *planMode)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func printStats(config model.Config) {
	planDir := config.Planning.Dir
	if planDir == "" {
		planDir = "~/.tack"
	}
	store, err := planning.NewStore(planDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	stats, err := store.LoadUsageStats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	if len(stats) == 0 {
		fmt.Println("No usage data yet.")
		return
	}

	type entry struct {
		cmd   string
		count int
	}
	var entries []entry
	for cmd, count := range stats {
		entries = append(entries, entry{cmd, count})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })

	fmt.Println("Command Usage Stats")
	fmt.Println(strings.Repeat("─", 40))
	for _, e := range entries {
		bar := strings.Repeat("█", min(e.count, 30))
		fmt.Printf("  %-20s %s %d\n", e.cmd, bar, e.count)
	}
}

func generateRecap(config model.Config) {
	planDir := config.Planning.Dir
	if planDir == "" {
		planDir = "~/.tack"
	}
	store, err := planning.NewStore(planDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	plan, err := store.LoadPlan()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading plan: %s\n", err)
		os.Exit(1)
	}

	var sb strings.Builder
	now := time.Now()
	_, week := now.ISOWeek()
	weekLabel := fmt.Sprintf("%d-W%02d", now.Year(), week)

	sb.WriteString(fmt.Sprintf("# Weekly Recap — %s\n\n", weekLabel))
	sb.WriteString("## Week Focus\n\n")
	for _, f := range plan.WeekFocus {
		title := f.Text
		if f.IssueNum > 0 {
			title = fmt.Sprintf("#%d %s", f.IssueNum, f.Text)
		}
		done := 0
		for _, s := range f.SubItems {
			if s.Done {
				done++
			}
		}
		progress := ""
		if len(f.SubItems) > 0 {
			progress = fmt.Sprintf(" [%d/%d]", done, len(f.SubItems))
		}
		sb.WriteString(fmt.Sprintf("- %s%s\n", title, progress))
	}

	sb.WriteString("\n## Completed\n\n")
	for _, item := range plan.Completed {
		sb.WriteString(fmt.Sprintf("- [x] %s\n", item.Text))
	}

	sb.WriteString("\n## Carry-Over\n\n")
	for _, item := range plan.Today {
		if !item.Done {
			sb.WriteString(fmt.Sprintf("- [ ] %s\n", item.Text))
		}
	}

	recap := sb.String()
	fmt.Print(recap)

	store.SaveRecap(weekLabel, recap)
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
