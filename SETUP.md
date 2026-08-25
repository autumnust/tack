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
gh repo clone autumnust/leisure_vault ~/leisure_vault
```

The planning data lives in `~/leisure_vault/tack/`:
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
# Optional: omit this when using Tack only for planning and local notes.
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
  dir: "~/leisure_vault/tack"
  max_week_focus: 3
EOF
```

Replace:
- `YOUR-ORG/projects/N` with your GitHub Project URL, or omit `project` to use planning and local notes without GitHub Projects access
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

## Cloud sync (Upstash Redis)

Tack can back planning data with an Upstash Redis store for fast cross-device
reads. When configured, Redis is the source of truth for `plan`,
`annotations`, `hibana`, `usage`, and `recaps`. The on-disk YAML/log/markdown
files are still written as offline-readable backups.

Keys:
- `tack:plan` / `tack:annotations` — JSON blobs, paired with
  `tack:plan:rev` / `tack:annotations:rev` counters for conflict detection
- `tack:hibana` — list of scratch notes (append-only)
- `tack:usage` — list of command usage entries (append-only)
- `tack:recap:<name>` — one key per weekly recap, indexed by `tack:recaps:index`

### Offline behavior

Writes that can't reach Redis land in a local `.outbox.jsonl` buffer and
replay on the next `tack` invocation. Append-only keys (usage, hibana) replay
straight through. Blob keys (plan, annotations, recaps) compare a cached
revision against the remote; if another device advanced the remote while you
were offline, the conflict is persisted under `.conflicts/` and tack refuses
to auto-resolve. The CLI/TUI status line shows `(redis: on, N conflicts — run
`tack --resolve-conflicts`)`.

Resolve conflicts interactively:

```bash
tack --resolve-conflicts
# per conflict: [l]ocal / [r]emote / [e]dit / [s]kip
```

Credentials can be set via env vars:

```bash
export UPSTASH_REDIS_REST_URL="https://<your-db>.upstash.io"
export UPSTASH_REDIS_REST_TOKEN="<rest-token>"
```

…or in `config.local.yaml`:

```yaml
planning:
  dir: ~/leisure_vault/tack
  redis_url: "https://<your-db>.upstash.io"
  redis_token: "<rest-token>"
```

Env vars win only when the config fields are empty. If neither is set, tack
runs file-only as before.

### One-time migration

To import existing `plan.yaml`, `annotations.yaml`, `usage.log`, and
`recaps/*.md` from a local directory (e.g. `~/leisure_vault/tack`) into Redis:

```bash
tack --migrate-leisure-vault ~/leisure_vault/tack
# add --force to overwrite existing Redis keys (including tack:usage)
```

## Notes

- `config.local.yaml` is gitignored — safe for real project URLs and team info
- `--plan` mode works entirely offline (no GitHub client required)
- Without Redis configured, planning data syncs via your vault's own mechanism (iCloud, git, etc.)
- Press `:h` inside tack for full keybinding reference
