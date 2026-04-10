package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	configPath := flag.String("config", "", "path to config file (default: config.local.yaml or config.yaml)")
	planMode := flag.Bool("plan", false, "start in planning mode")
	scratchText := flag.String("scratch", "", "add a scratch note and exit (e.g. --scratch \"idea\")")
	statsMode := flag.Bool("stats", false, "print command usage stats and exit")
	recapMode := flag.Bool("recap", false, "generate weekly recap and exit (for cron)")
	whichConfig := flag.Bool("which-config", false, "print which config file would be loaded and exit")
	flag.Parse()

	// Auto-detect config: prefer config.local.yaml (gitignored) over config.yaml
	// Check both CWD and the directory containing the binary.
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = findConfig()
	}
	if cfgPath == "" {
		fmt.Fprintln(os.Stderr, "No config file found. Searched:")
		fmt.Fprintln(os.Stderr, "  1. ./config.local.yaml and ./config.yaml (current directory)")
		if exe, err := os.Executable(); err == nil {
			fmt.Fprintf(os.Stderr, "  2. %s/config{.local,}.yaml (binary directory)\n", filepath.Dir(exe))
		}
		if home, err := os.UserHomeDir(); err == nil {
			fmt.Fprintf(os.Stderr, "  3. %s/.tack/config{.local,}.yaml\n", home)
		}
		fmt.Fprintln(os.Stderr, "\nCreate a config file or use --config <path>.")
		os.Exit(1)
	}

	// Resolve to absolute path for reliable display and save-back
	absCfgPath, err := filepath.Abs(cfgPath)
	if err == nil {
		cfgPath = absCfgPath
	}

	if *whichConfig {
		fmt.Printf("Config: %s\n", cfgPath)
		if data, err := os.ReadFile(cfgPath); err == nil {
			fmt.Printf("---\n%s", data)
		} else {
			fmt.Printf("Error reading: %s\n", err)
		}
		return
	}

	config, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config (%s): %s\n", cfgPath, err)
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
	if *scratchText != "" {
		addScratch(config, *scratchText)
		return
	}

	client, err := ghclient.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(config, cfgPath, client, *planMode)
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

func addScratch(config model.Config, text string) {
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
	plan.Scratch = append(plan.Scratch, model.ScratchNote{
		Text:      text,
		CreatedAt: time.Now(),
	})
	if err := store.SavePlan(plan); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Scratch added: %s\n", text)
}

func findConfig() string {
	// Check CWD first
	if _, err := os.Stat("config.local.yaml"); err == nil {
		return "config.local.yaml"
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// Check directory of the binary
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		local := filepath.Join(dir, "config.local.yaml")
		if _, err := os.Stat(local); err == nil {
			return local
		}
		base := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(base); err == nil {
			return base
		}
	}

	// Check ~/.tack/
	home, err := os.UserHomeDir()
	if err == nil {
		local := filepath.Join(home, ".tack", "config.local.yaml")
		if _, err := os.Stat(local); err == nil {
			return local
		}
		base := filepath.Join(home, ".tack", "config.yaml")
		if _, err := os.Stat(base); err == nil {
			return base
		}
	}

	// No config found anywhere
	return ""
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
