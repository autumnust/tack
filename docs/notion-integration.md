# Notion as a storage backend (design notes)

**Status:** Code merged but inactive. `planning.backend` is currently `redis`
in `config.local.yaml`; switch to `notion` to re-enable. Notion-side data
exists under the **Tack Space** parent page and was not deleted on revert.

These notes preserve the *why* behind the integration so a future round can
pick up the same decisions or knowingly reverse them.

---

## Why a second backend at all

Hibana (scratch notes) was the original motivator: the user wanted a
**mobile-friendly entry point** for capture, and Notion's mobile app gives
that essentially for free. Upstash works but has no native mobile UX.

Once the seam was open, the rest of the keys (`tack:plan`,
`tack:annotations`, recaps, usage) followed for symmetry — there was no
strong reason to keep some keys on Upstash and others on Notion.

## Backend selection

`planning.backend` in config picks the active backend:

| Value     | Effect                                                |
|-----------|-------------------------------------------------------|
| `notion`  | Default when unset. Bootstraps databases under Tack Space. |
| `redis`   | Legacy Upstash REST path (`internal/planning/redis.go`).   |
| `none`/`local`/`off` | Local-only; no cloud writes.                |

Both backends implement the same `redisBackend` interface
(`internal/planning/redis.go`). The interface name is legacy — Notion satisfies
it via `notionBackend` (`internal/planning/notion.go`).

## Schema decisions

Everything lives under one shared parent page called **Tack Space**.
Bootstrap discovers it by title or accepts an explicit
`planning.notion.parent_page_id`. Discovered IDs are cached in
`<planDir>/.notion.json` so subsequent runs don't re-search.

Four child databases:

| Database | Holds                | Title prop | Other props                              |
|----------|----------------------|------------|------------------------------------------|
| Hibana   | `tack:hibana` rows   | `Name` (first line) | `Body` (rest), `CreatedAt` (rich_text RFC3339Nano), `UpdatedAt` (date) |
| Usage    | `tack:usage` entries | `Command`  | `Timestamp` (date)                       |
| Recaps   | `tack:recap:*`       | `Name`     | body content lives in page-body code block |
| Blobs    | `tack:plan`, `tack:annotations`, all `*:rev` counters | `Key` | `Value` (rich_text), `UpdatedAt` (date); large payloads spill to a page-body code block |

### Decisions inside the schema

- **Why `CreatedAt` is rich_text, not date.** Notion `date` props lose
  sub-second precision. The hibana merge logic uses `CreatedAt` as a stable ID,
  so byte-identical round-trip matters. RFC3339Nano sorts lexicographically
  in the same order as time order.

- **Why one Blobs DB and not separate Plan / Annotations DBs.** The set of
  scalar keys is small but open-ended (one `:rev` per blob plus future
  additions). One KV-shaped DB scales without schema churn.

- **Why bodies live in a code block (`Blobs`, `Recaps`) but properties
  (`Hibana`).** Notion rich_text caps at 2000 chars per element. Anything
  potentially larger (plan JSON, recap markdown) goes in a code block split
  across multiple rich_text fragments. Hibana notes are typically small
  enough to fit a property and stay queryable in bulk.

## Hibana title/body split with mobile-edit reconciliation

The most subtle part of the integration. See `notion.go:resolveHibanaBody`.

**Schema:** title = first line; `Body` property = the rest; page-body
paragraph blocks = the rest, mirrored, for Notion-mobile prettiness.

**Read priority:**

1. **Legacy migrated row** (Body empty AND title contains `\n`): use title
   verbatim. Don't fetch children. The upstash→notion migration produced
   these by stuffing full multi-line text into the title.
2. **First-seen row, non-empty Body**: trust property without children fetch.
3. **First-seen row, empty Body**: fetch children → write-through Body
   property. This catches pages **created in Notion** with body content but
   no property data.
4. **Remote-edited row** (cached LET ≠ current LET): fetch children →
   write-through. Catches mobile edits to existing rows.
5. **Steady state** (LET unchanged): use Body property. No extra call.

**Cached state:** per-page `last_edited_time` map persisted at
`<planDir>/.notion_page_revs.json`. We compare current LET vs the value we
wrote ourselves; mismatch ⇒ remote edit.

**Trade-offs accepted:**

- *Mobile deletes don't propagate to local.* Hibana is TUI-authoritative.
  Adds and edits on mobile flow back; deletes on mobile do not. Decision
  made because the user wants mobile as a capture/edit inbox, not a
  prune surface.
- *Mobile edits made before the very first reconcile may be missed for
  rows where Body is non-empty.* If somebody edited a row's page body before
  we ever cached its LET, branch 2 trusts the property. Branch 3 covers the
  more common case (Body empty → fetch).
- *Legacy migrated rows aren't eagerly upgraded.* The first edit on each
  row will produce the new format; until then they keep multi-line titles.

To force a full re-reconcile (e.g., after suspecting drift), delete
`.notion_page_revs.json`. Worst-case cost is one children-fetch per row.

## Hibana save path: delta over full rewrite

The original Redis path on `SavePlan` was DEL + RPUSH the whole list. That
costs N API calls under Notion every TUI quit, which is unacceptable.

Solution: optional `hibanaReplacer` interface. Backends that can do
per-row delta (Notion) implement it; Redis doesn't and falls back to full
rewrite. The store's `rewriteHibanaList` prefers delta when available.

**Budget:** 2 seconds per save. Anything not applied gets spilled into the
existing outbox as a `replace_list` op — `Reconcile()` already handles
those. Set in `store.go:hibanaSaveBudget`.

## Optimistic concurrency caveat

Notion has no compare-and-swap. Our `Incr(rev)` is a read-modify-write that
can race with a concurrent device. For a single-user multi-device setup
this is rare enough to accept; the conflict-file mechanism in
`reconcile.go` still catches the cases that *do* slip through.

## Latency baseline (~22 hibana rows)

| Path                                       | Approx wall time |
|--------------------------------------------|------------------|
| Bootstrap (cached IDs)                     | <500ms           |
| Bootstrap (cold — searches for Tack Space) | ~1.5s            |
| `LoadPlan` steady state                    | ~3.4s            |
| `LoadPlan` first-ever (cold rev cache)     | ~3.4s + N×0.35s for empty-Body rows |
| TUI quit save (1–3 deltas)                 | <1s              |

The throttle in `internal/notion/client.go` is ~350ms between requests to
stay under Notion's documented 3 req/s soft limit.

## Coexistence with Redis

Redis remains fully operational under `backend: redis`. The migration tool
(`tack --migrate-upstash-to-notion`) is one-way and idempotent: re-running
overwrites Notion with whatever's in Upstash, leaving Upstash untouched.
There is no notion→upstash migration path because we never needed one.

## Configuration shape

```yaml
planning:
    dir: /path/to/local/cache
    backend: notion           # or redis, or none
    notion:
        token: ntn_...        # or set NOTION_API_KEY env (also read from .env)
        parent_page_id: ""    # optional; auto-discovered from "Tack Space" title
    redis_url: ...            # only used when backend == redis
    redis_token: ...
```

Notion bootstrap is idempotent and additive: the `Body` rich_text property
gets PATCHed onto an existing Hibana DB if missing, so re-running against
an older space is safe.

## Things explicitly *not* done

- **Notion → Upstash migration.** No use case for it.
- **Recap body fetch parallelism.** Could reduce cold-load time but not
  worth the rate-limiter complexity.
- **Block-type-aware page-body reading.** `readPageBodyText` only
  understands paragraph blocks. Bullets, headings, etc. on mobile would
  serialize as empty lines. Acceptable while hibana remains plain-text.
- **Per-property granular conflict detection.** Notion's `last_edited_time`
  is page-level; if we ever add structured fields to hibana (tags, etc.)
  the current reconcile path can't tell which one changed.

## Reverting

To switch off Notion in code (not just config), the seam is small:

- `OpenStore` in `internal/planning/open.go` — drop the `notion` case.
- `BackendLabel` in `store.go` — drop the `*notionBackend` case.
- `internal/notion/`, `notion.go`, `notion_bootstrap.go`,
  `notion_revs.go`, `notion_exports.go`, `upstash_to_notion.go`, and
  the `--migrate-upstash-to-notion` flag in `main.go` can be deleted.
- Per-call timeout in `store.go:ctx()` can revert to `3 * time.Second`
  once Notion is gone (15s was bumped to accommodate Notion's throttle).

This document and the rev-cache file (`<planDir>/.notion_page_revs.json`)
are also removable.
