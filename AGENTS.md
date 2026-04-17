# AGENTS.md

## Testing Workflow

- Treat tests as the executable specification for behavior.
- Prefer adding or updating a focused test first before changing implementation.
- Assume a new behavior test should fail until the implementation proves otherwise.
- After adding a test, run the narrowest relevant test target first, then widen to package and full-suite runs.
- If a test exposes a bug, fix the production code immediately and rerun the tests until they pass.
- Do not change a test merely to make it pass unless the expected behavior is genuinely wrong or outdated.
- If a test appears to need a semantic change, continue with other valid work and surface that proposed test change to the user at the end of the session.

## Practical Expectations

- Favor small, deterministic unit tests around parsing, data transforms, caching, and boundary behavior.
- Keep app-level or workflow-style tests clearly separated from unit tests when possible.
- When adding coverage around external integrations, mock the boundary and assert observable behavior: requests sent, errors surfaced, and parsed results returned.
