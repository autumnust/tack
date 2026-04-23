package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	hibanaText := flag.String("hibana", "", "add a quick note and exit (e.g. --hibana \"idea\")")
	statsMode := flag.Bool("stats", false, "print command usage stats and exit")
	recapMode := flag.Bool("recap", false, "generate weekly recap and exit (for cron)")
	whichConfig := flag.Bool("which-config", false, "print which config file would be loaded and exit")
	migrateLeisureVault := flag.String("migrate-leisure-vault", "", "one-time: import plan.yaml/annotations.yaml from <path> into Redis and exit")
	migrateForce := flag.Bool("force", false, "with --migrate-leisure-vault, overwrite existing Redis keys")
	resolveConflicts := flag.Bool("resolve-conflicts", false, "interactively resolve offline-sync conflicts and exit")
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
	if *hibanaText != "" {
		addHibana(config, *hibanaText)
		return
	}
	if *migrateLeisureVault != "" {
		runMigration(config, *migrateLeisureVault, *migrateForce)
		return
	}
	if *resolveConflicts {
		runResolveConflicts(config)
		return
	}

	client, err := ghclient.NewClient()
	if err != nil && !*planMode {
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

// resolveRedisCreds returns (url, token), preferring config values and falling
// back to UPSTASH_REDIS_REST_URL / UPSTASH_REDIS_REST_TOKEN env vars. Empty
// strings mean "no Redis backend".
func resolveRedisCreds(config model.Config) (string, string) {
	url := config.Planning.RedisURL
	if url == "" {
		url = os.Getenv("UPSTASH_REDIS_REST_URL")
	}
	token := config.Planning.RedisToken
	if token == "" {
		token = os.Getenv("UPSTASH_REDIS_REST_TOKEN")
	}
	return url, token
}

func openStore(config model.Config) (*planning.Store, error) {
	planDir := config.Planning.Dir
	if planDir == "" {
		planDir = "~/.tack"
	}
	url, token := resolveRedisCreds(config)
	return planning.NewStoreWithRedis(planDir, url, token)
}

func printStats(config model.Config) {
	store, err := openStore(config)
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
	store, err := openStore(config)
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

func addHibana(config model.Config, text string) {
	store, err := openStore(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	note := model.ScratchNote{Text: text, CreatedAt: time.Now()}
	if err := store.AddHibana(note); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Hibana: %s  %s\n", text, redisStatus(store))
}

// redisStatus returns a one-line indicator suitable for trailing any CLI output.
func redisStatus(store *planning.Store) string {
	if !store.RedisEnabled() {
		return "(redis: off — local only)"
	}
	pending := store.OutboxPending()
	conflicts := 0
	if cs, err := store.LoadConflicts(); err == nil {
		conflicts = len(cs)
	}
	switch {
	case conflicts > 0:
		return fmt.Sprintf("(redis: on, %d conflicts — run `tack --resolve-conflicts`)", conflicts)
	case pending > 0:
		return fmt.Sprintf("(redis: on, %d pending)", pending)
	default:
		return "(redis: on)"
	}
}

func runResolveConflicts(config model.Config) {
	store, err := openStore(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	if !store.RedisEnabled() {
		fmt.Fprintln(os.Stderr, "No Redis backend configured; nothing to resolve.")
		os.Exit(1)
	}
	conflicts, err := store.LoadConflicts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading conflicts: %s\n", err)
		os.Exit(1)
	}
	if len(conflicts) == 0 {
		fmt.Println("No unresolved conflicts.")
		return
	}

	reader := bufio.NewReader(os.Stdin)
	for _, c := range conflicts {
		fmt.Printf("\n── Conflict on %s (local rev %d vs remote rev %d)\n", c.Key, c.CachedRev, c.RemoteRev)
		fmt.Println("   [l]ocal  [r]emote  [e]dit  [s]kip")
		fmt.Printf("   local : %s\n", oneLine(c.LocalPayload))
		fmt.Printf("   remote: %s\n", oneLine(c.RemotePayload))
		fmt.Print("> ")
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(strings.ToLower(choice))
		switch choice {
		case "l", "local":
			if err := pushConflictResolution(store, c.Key, c.LocalPayload); err != nil {
				fmt.Fprintf(os.Stderr, "push failed: %s\n", err)
				continue
			}
			_ = store.ClearConflict(c.Key)
			_ = store.DropOutboxForKey(c.Key)
			fmt.Println("   → local kept")
		case "r", "remote":
			_ = store.DropOutboxForKey(c.Key)
			_ = store.ClearConflict(c.Key)
			fmt.Println("   → remote kept")
		case "e", "edit":
			merged, err := runEditor(c)
			if err != nil {
				fmt.Fprintf(os.Stderr, "edit failed: %s\n", err)
				continue
			}
			if err := pushConflictResolution(store, c.Key, merged); err != nil {
				fmt.Fprintf(os.Stderr, "push failed: %s\n", err)
				continue
			}
			_ = store.ClearConflict(c.Key)
			_ = store.DropOutboxForKey(c.Key)
			fmt.Println("   → merged version pushed")
		default:
			fmt.Println("   → skipped")
		}
	}
}

// pushConflictResolution writes the resolved payload directly, bypassing the
// outbox (we know we're online since we just read remote). Uses the same rev
// semantics as a normal write so other devices see the bump.
func pushConflictResolution(store *planning.Store, key, payload string) error {
	return store.ResolveBlob(key, payload)
}

func runEditor(c planning.Conflict) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "tack-conflict-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	fmt.Fprintf(f, "<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE\n", c.LocalPayload, c.RemotePayload)
	f.Close()

	cmd := exec.Command(editor, f.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	// Strip conflict markers if the user left them.
	out := stripConflictMarkers(string(b))
	return out, nil
}

func stripConflictMarkers(s string) string {
	var out []string
	skip := false
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			skip = false // keep local by default if markers left intact
		case strings.HasPrefix(line, "======="):
			skip = true
		case strings.HasPrefix(line, ">>>>>>>"):
			skip = false
		default:
			if !skip {
				out = append(out, line)
			}
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func runMigration(config model.Config, srcDir string, force bool) {
	store, err := openStore(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	if !store.RedisEnabled() {
		fmt.Fprintln(os.Stderr, "Error: no Redis backend configured (set UPSTASH_REDIS_REST_URL / UPSTASH_REDIS_REST_TOKEN or planning.redis_url/redis_token in config)")
		os.Exit(1)
	}
	res, err := store.MigrateLeisureVault(srcDir, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("Migration complete: plan=%d, annotations=%d rows, hibana=%d notes\n", res.PlanItems, res.AnnotationRows, res.HibanaNotes)
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
