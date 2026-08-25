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
| 2026-08-25 | a task in a repo Claude Code never opened sits at the trust dialog (nothing to inherit trust from) | vera fleet | now | fixed: Vera trusts the room she made |
| 2026-08-25 | /say streams delta/status/error/done only — a TUI over HTTP cannot show tool calls as cards | vera wire | now | fixed: tool_call/tool_result frames |
| 2026-08-25 | an agent mid-task posts no status for 12+ minutes; Vera reads "running" from pane activity, the person reads nothing | vera brief | later | ask for a status per milestone, or derive one from the pane title |
