# mote

A small agent harness, in Go. The thing Vera is built on; usable
without her.

## What it is

pi's shape, ours: a harness is a loop over a model with tools, a
record of what happened, and a terminal to watch it in. Everything
that makes one agent different from another — its prompt, which
tools it has, what those tools are allowed to touch — is a *profile*,
not code in the loop. Vera is a profile (the supervisor: no writes
outside her own home, work goes to rooms she watches). A coding agent
is another. A profile is a directory a person can read.

## Pieces

- `agent` — the loop: messages, tool rounds, streaming, providers
  (OpenAI-compatible first; Anthropic-native next).
- `tool` — a registry with policy: each tool declares what it does;
  each profile says auto / ask / deny, by path where a path is
  involved. The boundary is wiring, not manners.
- `session` — the record on disk (Vera's journal is the seed): one
  file per conversation, jsonl, append-only, holding what the terminal
  needs to redraw a transcript exactly.
- `tui` — the terminal: streaming markdown, tool calls as cards,
  a side pane, a status line, a real input. Driven through one
  interface so any agent — local, or Vera over HTTP — sits behind it.
- `cmd/mote` — the harness alone, with a profile directory.

## Milestones

1. The TUI, behind an interface, with vera's chat as the first client.
2. Sessions on disk, tools that stream their output, and what a turn
   cost.
3. What driving Vera's chat on it turned up: nothing asks the terminal
   a question, markdown at the width it was given, a rail that says
   what it dropped, a notice that can be about a thing, and a line of
   the application's own on the status bar.
4. The charm v2 stack: the frame is a `tea.View`, the cursor in the
   box is the terminal's own, and what colour the terminal is is asked
   for without waiting — so the first frame is immediate even on a
   terminal that never answers.

Read `GAPS.md` for what the loop of building it through Vera turned
up, and how each gap was classified.

## Try it

```
go run ./cmd/mote demo        # the terminal, over a scripted agent
go run ./cmd/mote sessions    # the conversations it left behind
go run ./cmd/mote demo -c <id>  # reopen one
```

Conversations live under `$XDG_STATE_HOME/mote/sessions`, or
`~/.local/state/mote/sessions`.
