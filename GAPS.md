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
| 2026-08-25 | milestone 1 landed and driven from a pane: streaming markdown (table, code), tool cards collapsed→expanded with args/result/duration/cost, notices, rail, status line all render | mote tui | tui | delivered (184a1100) |
| 2026-08-25 | Vera's chat shows two panels at once (fleet strip + F2 belief); tui offers one rail | mote tui | tui | closed by the port: belief goes behind /debug as markdown; not needed |
| 2026-08-25 | input history and the transcript die with the process | mote session | later | fixed: `session` on disk, `Options.Session`, `mote demo -c` (598e3374) |
| 2026-08-25 | a long tool's output arrives whole; no `tool_output` kind to stream into an open card | mote agent | later | fixed: `agent.KindToolOutput` streams into the open card (598e3374) |
| 2026-08-25 | no turn or session cost total, only per call | mote tui | later | fixed: the status line carries the turn's, then the conversation's (598e3374) |
| 2026-08-25 | the `rook` CLI has no type/send verb — driving a pane from a script needs vera's mux backend (scratch `muxctl`) | rook | later | a `rook send <id> <text>` verb, or `vera pane` verbs |
| 2026-08-25 | ~/.claude/skills/rook describes the old native app (`rook dump`, `rook split`), not the engine | rook skill | later | rewrite the skill from `rook help` |
| 2026-08-25 | esc mid-tool left the card spinning forever; nothing resolved the call | mote tui | tui | fixed while building milestone 2: a call the turn ended without reads as stopped (598e3374) |
| 2026-08-25 | Go has `os.UserConfigDir` and `os.UserCacheDir` but no state one | go | later | `cmd/mote/state.go` does XDG by hand; delete it when Go ships `os.UserStateDir` |
| 2026-08-25 | a notice that arrives between exchanges is not recorded, so a reopened transcript loses it | mote session | later | deliberate — the file is the conversation, not the chrome. Revisit if a person misses one |
| 2026-08-25 | no `mote dump <id>` to read a conversation without the terminal | mote session | later | the file is jsonl and `Turn.Answered()` exists; it is a printer, not a design |
| 2026-08-25 | vera cannot `require github.com/incantery/mote` until mote is on GitHub (go.work resolves locally, `go mod tidy`/`go install @latest` do not) | repos | now | needs the repo published |
| 2026-08-25 | the rail truncates at the pane height and does not say what it dropped — "four tasks" vs "four of nine" | mote tui | later | fixed: the last line is `+N more` when there is more than there is room for (065c8b59) |
| 2026-08-25 | the conversation id is write-only (`SetConversation`); the app keeps its own copy for /dump | mote tui | later | fixed: `Options.OnConversation` and `Model.Conversation()` (065c8b59) |
| 2026-08-25 | verad's `run` id (for reattaching a dropped exchange) has no seam in `agent.Agent` | mote agent | later | needed off loopback |
| 2026-08-25 | a notice has no identity; a task that changed twice says both | mote agent | later | fixed: `agent.About(id, text)` rewrites the line the last one left (065c8b59) |
| 2026-08-25 | no permanent one-liner for focus/runs-in-flight beside the model name | mote tui | later | fixed: `Options.StatusRight`, polled with the rail; the key hints give way to it (065c8b59) |
| 2026-08-25 | vera's chat is on mote/tui (c8ee7ae8): agent over /say incl. tool frames, fleet as rail, commands, notices; 822→544 lines, none layout | vera chat | tui | delivered |
| 2026-08-25 | vera requires a private mote; `go run …/cmd/vera@latest` on-ramp in vera's READMEs is now false for anyone without access | repos | now | decision: mote public, or drop the on-ramp line |
| 2026-08-25 | vera chat on mote, driven live: fleet tool card (5ms) opens to args/result, reply as markdown, rail live | vera chat | tui | verified by use |
| 2026-08-25 | a terminal query reply (`[1;1R`, cursor position) is typed into the input on start — glamour `auto` style/termenv queries the terminal after Bubble Tea owns stdin | mote tui | now | worse than a leak: under a pty that never answers, the demo wrote the two queries and drew nothing. Fixed: nothing mote draws asks the terminal anything — `resolveStyle` settles the style in `New` from GLAMOUR_STYLE/profile/COLORFGBG, and the renderer is told the answer so AdaptiveColor stops asking (065c8b59) |
| 2026-08-25 | the greeting wraps mid-sentence around inline code (`/help` / `has` / `the keys`) | mote tui | tui | fixed: the document margin and the inline-code padding are taken back, so the wrap happens at the width the renderer was given (065c8b59) |
| 2026-08-25 | bubbletea v1 asks the terminal for its background in its own package `init`, before `main`: a terminal that never answers costs one five-second stall before the first frame | mote tui | later | nothing in-process runs before an imported package's init. Gone in bubbletea v2; until then it is one bounded stall, and mote adds none of its own |
| 2026-08-25 | a notice replaced in place changes the transcript above the fold; scrolled away, the person never sees it happen | mote tui | later | deliberate — one line per task is what was asked for. Revisit if a person loses track of what changed |
| 2026-08-25 | milestone 3 landed (065c8b59): style decided without asking, markdown at full width, rail "+N more", conversation id readable, notices with identity, StatusRight; goldens explained | mote tui | tui | delivered |
| 2026-08-25 | the one start-up stall left is bubbletea v1's package init asking lipgloss for the background — 5 s once on a terminal that never answers OSC 11, microseconds on one that does | mote tui | later | rook answers OSC 10/11 (requests/2026-08-25-answer-osc-11.md); or bubbletea v2 |
