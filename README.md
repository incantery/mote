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
  each profile says allow / ask / deny, by path where a path is
  involved. The boundary is wiring, not manners. `tool/builtin` is
  the seven a coding agent cannot do without — read, write, edit,
  delete, list, search, run.
- `profile` — a directory a person can read: `profile.md` for the
  prompt, `policy.toml` for the rules. `profiles/supervisor` is the
  worked example.
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
5. Tools with policy, profiles, and the ask: what a harness calls, and
   what it is allowed to call it with.

## Tools, policy, the ask

A tool is a name, a description, a JSON Schema and something to run:

```go
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, args json.RawMessage, out io.Writer) (Result, error)
}
```

A tool that takes paths says which arguments are paths (`Paths`); one
that runs a command says what the command line is (`Command`). That is
all a policy needs, and it is why a profile can write rules about
tools it has never heard of.

`Registry.Definitions()` is the set in the OpenAI function-tool shape,
which is what goes into the request body. What comes back is a tool
call, and the harness asks:

```go
call := tool.NewCall(id, t, args)
switch v := gate.Decide(call); v.Decision {
case tool.Deny:
	// v.Reason is the profile's own sentence; tell the model that.
case tool.Ask:
	reply(agent.Asking(id, t.Name(), string(args), v.Reason))
	ok, err := gate.Wait(ctx, call)   // parks until the person answers
}
res, err := t.Run(ctx, args, out)     // out becomes tool_output events
```

Deciding touches no files. Every path is made absolute and cleaned
first, so a `../` cannot walk around a rule; an allow needs *every*
path of a call, a deny or an ask needs *one*; a command prefix matches
on a word boundary and never through a shell operator.

The ask reaches the terminal as `agent.KindAsk` and comes back through
`agent.Answerer` — `yes`, `no`, or `always`. An `always` is remembered
by `tool.Gate` as a grant with a reach: the directory for a file, the
program for a command.

## A profile

```
profiles/supervisor/
  profile.md     the system prompt, with name / model / tools on top
  policy.toml    the rules, in the order they are tried
```

`profile.Load(dir)` returns the prompt, the tool names and the policy;
a typo in the rules is an error there rather than a surprise at
midnight. `profiles.Supervisor()` is the same directory, compiled in.

Read `GAPS.md` for what the loop of building it through Vera turned
up, and how each gap was classified.

## Try it

```
go run ./cmd/mote demo        # the terminal, over a scripted agent
go run ./cmd/mote sessions    # the conversations it left behind
go run ./cmd/mote demo -c <id>  # reopen one
```

In the demo, say a line with **policy** in it: nine real tool calls
against this checkout, decided by `profiles/supervisor` — five
allowed, one denied ("start a task for that"), two that stop and ask,
and a `delete` under her own home that does not. `/policy` prints the
rules. The demo's `~` is a scratch directory it deletes when it
quits, so the writes are real and land nowhere real.

A call that does not run — denied, or asked about and refused — comes
back as `error: nothing was done: <why>` (`tool.Refused`,
`tool.Declined`), because a reason on its own reads like advice
beside a write that went through.

Conversations live under `$XDG_STATE_HOME/mote/sessions`, or
`~/.local/state/mote/sessions`.
