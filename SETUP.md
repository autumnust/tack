# Tack Setup Guide

## Prerequisites

- Go 1.21+
- GitHub CLI (`gh`) authenticated: `gh auth login`
- An Obsidian vault (or any synced folder) for planning data

## Quick Setup

### 1. Build and install

```bash
cd /path/to/tack
go install .
```

Ensure `~/go/bin` is in your PATH. Add to `~/.zshrc` if not:

```bash
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.zshrc
source ~/.zshrc
```

### 2. Clone your Obsidian vault

Tack stores planning data (plan.yaml, annotations, inbox) in a configurable directory. For synced planning across machines, use an Obsidian vault or similar:

```bash
gh repo clone autumnust/leisure_vault ~/Documents/leisure_vault
```

The planning data lives in `~/Documents/leisure_vault/tack/`:
- `plan.yaml` — weekly goals, today items, scratch notes, completed archive
- `annotations.yaml` — private notes on GitHub issues
- `inbox.yaml` — incoming items
- `usage.log` — command usage stats

### 3. Create config

Tack searches for config in this order:
1. `./config.local.yaml` (current directory)
2. `./config.yaml` (current directory)
3. `<binary-dir>/config.local.yaml`
4. `~/.tack/config.local.yaml`
5. `~/.tack/config.yaml`

For global access from any directory, place config in `~/.tack/`:

```bash
mkdir -p ~/.tack
cat > ~/.tack/config.local.yaml << 'EOF'
project: "https://github.com/orgs/YOUR-ORG/projects/N"

focus:
  - 100   # issue numbers to track
  - 200

team:
  - login: your-github-username
    name: Your Name
  - login: teammate
    name: Teammate Name

status_field: "Status"

planning:
  dir: "~/Documents/leisure_vault/tack"
  max_week_focus: 3
EOF
```

Replace:
- `YOUR-ORG/projects/N` with your GitHub Project URL
- `focus` numbers with the epic/issue numbers you want to track
- `team` entries with your actual team
- `planning.dir` with the path to your vault's tack directory

### 4. Verify

```bash
tack --which-config   # should show ~/.tack/config.local.yaml
tack --plan           # planning mode (works without GitHub)
tack --stats          # usage stats (works without GitHub)
tack                  # full board mode (requires GitHub auth + valid project URL)
```

## Modes

- `tack` — Board mode: standup kanban backed by GitHub Projects
- `tack --plan` — Planning mode: weekly goals, today tasks, scratch notes. No GitHub needed.
- `tack --scratch "idea"` — Quick add a scratch note from CLI
- `tack --stats` — Print command usage stats
- `tack --recap` — Generate weekly recap markdown

## Notes

- `config.local.yaml` is gitignored — safe for real project URLs and team info
- `--plan` mode works entirely offline (no GitHub client required)
- Planning data syncs via your Obsidian vault's sync mechanism (iCloud, git, etc.)
- Press `:h` inside tack for full keybinding reference
