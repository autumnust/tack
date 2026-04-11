# Contributing to Tack

## Development Workflow

### Testing Requirements

**Every feature addition or bug fix must include workflow tests.**

The test suite in `internal/tui/workflow_test.go` uses a test harness that:
- Creates an app pre-populated with test data (no network needed)
- Sends keystroke sequences to simulate real user interaction
- Asserts on view state, status messages, ops queue, and data

### Adding a New Workflow Test

1. Add a new `TestXxx` function in `internal/tui/workflow_test.go`
2. Use `newTestApp()` for a fresh app with test data
3. Use `sendKeys(t, app, "j", "enter", ...)` for navigation
4. Use `sendCommand(t, app, "mv #101 done")` for `:` commands
5. Assert with `assertView()`, `assertStatus()`, `assertOpsLen()`

Example:
```go
func TestMyFeature(t *testing.T) {
    app := newTestApp()
    app = sendCommand(t, app, "plan")
    assertView(t, app, viewPlan)
    // ... test the feature workflow
}
```

### When to Add Tests

- **Bug fix**: Add a test that reproduces the bug (fails without fix, passes with)
- **New command**: Test the command works, and test edge cases (no selection, wrong args)
- **New view/mode**: Test entering and exiting the mode from all possible entry points
- **Interaction chains**: Test multi-step workflows (the most common source of bugs)

### Running Tests

```bash
make test        # Run all tests
make build       # Build binary
make check       # Run tests + build
make release     # Run tests, build, tag, push
```

Or directly:
```bash
go test ./... -v
```

### Release Process

1. Ensure all tests pass: `make test`
2. Build: `make build`
3. Tag and push: `make release VERSION=v0.3`

The `make release` target refuses to proceed if tests fail.

### Test Data

The test fixtures in `workflow_test.go` include:
- 3 team members (alice, bob, charlie)
- 2 epics (#100, #200) with children
- 1 standalone issue (#300)
- Status field with Todo/In Progress/Done options
- ChildrenMap with sub-issues

If your feature needs additional test data, extend `testProject()` or `testConfig()`.

### Unit Test Files

Beyond workflow tests, unit tests exist for pure-logic packages:

- `internal/tui/command_test.go` — tokenize, parseCommand
- `internal/tui/ops_test.go` — OpQueue, ExecuteChecked (with mock client)
- `internal/grouping/strategy_test.go` — ByEpic, ByLabel, GroupByPerson
- `internal/planning/store_test.go` — Store I/O, Rollover, usage stats

Use `newTestAppWithStore(t)` for tests that need a planning store (stats, recap).
