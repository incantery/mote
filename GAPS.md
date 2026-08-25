# Gaps

Found while building mote *through* Vera — starting tasks, reading
reports, using the chat — and classified as they came up:

- **now** — blocks the loop, or a supervisor bug: fixed in vera before going on
- **tui** — the proper TUI fixes it; noted so the TUI is judged against it
- **later** — a harness feature (policy, providers, MCP): mote's backlog

| when | gap | where | class | status |
|------|-----|-------|-------|--------|
| 2026-08-25 | resuming a task already at `finished` restarts its agent to say "still done" | vera fleet | now | fixed: done/failed is at rest whatever became of the pane |
| 2026-08-25 | driving Vera from a script needs curl + the identity secret | vera cli | now | fixed: `vera say`, `vera tasks`, `vera task <verb>` |
| 2026-08-25 | a brief started through the mind is paraphrased by the model on its way into the task | vera mind | now | worked around: `vera task start … -` sends it verbatim; the mind's `start` should carry the person's words unchanged — later |
