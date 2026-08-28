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

- `agent` — the seam a terminal sees: say a thing, get a stream of
  events back until one of them says done. Nothing in it mentions a
  provider.
- `provider` — the wire, behind one interface: `Stream(ctx, Request,
  func(Event)) (Usage, error)`. OpenAI-compatible chat completions and
  the Anthropic Messages API, and `New` picks between them from a
  profile's `model:` line.
- `tool` — a registry with policy: each tool declares what it does;
  each profile says allow / ask / deny, by path, by command, or by an
  argument where the argument is the question. The boundary is wiring,
  not manners. `tool/builtin` is the seven a coding agent cannot do
  without — read, write, edit, delete, list, search, run.
- `mcp` — other people's tools: the servers a profile declares in
  `mcp.toml`, connected, with everything they offer in the same
  registry under the same policy.
- `profile` — a directory a person can read: `profile.md` for the
  prompt, `policy.toml` for the rules. `profiles/supervisor` is the
  worked example.
- `session` — the record on disk (Vera's journal is the seed): one
  file per conversation, jsonl, append-only, holding what the terminal
  needs to redraw a transcript exactly.
- `tui` — the terminal: streaming markdown, tool calls as cards, a
  side pane, a status line, a real input, and six registers so that
  none of them reads like another. Driven through one interface so any
  agent — local, or Vera over HTTP — sits behind it.
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
7. Providers: one interface over two wires, so the loop stops owning
   the socket.
8. MCP, and what the first harness asked of the tool package: a run
   handle, `Result.Meta`, rules that key on an argument, tools the
   harness owns.
9. Registers: six things a transcript can say, and none of them
   looking like another.

## The transcript

Everything in the transcript is in one of six registers, and no two of
them look alike:

| register | what it is | how it reads |
| --- | --- | --- |
| you | the person's turn | warm, bold, in the margin |
| reply | the model's prose | full width, ordinary weight |
| tool | a call | one sentence closed, `ctrl+o` for args and result |
| event | a notice from outside the exchange | dim, indented, consecutive ones grouped |
| result | what a command printed | `Note` dim like an event, `Show` down its own gutter |
| error | a failure | red, one line |

An exchange — what was said, what came back, and every card opened on
the way — is one block with single spacing inside it, and a thin rule
is the only thing that separates two of them. `Options.Timestamps`
puts the time on that rule.

A closed tool card is a sentence, not a dump of arguments:

```
▸ ✓ fleet · started a ship task in vera → 05a40191 · 473ms
```

The sentence is `agent.Event.Summary`, which the harness fills in
because the harness is the only one who can — the terminal has the
JSON and nothing else. `Call(id, name, args).WithSummary("…")` puts
one on, on the call or on its result; without one the terminal reads
the argument values in the order they were written.

The colour is still ANSI 1–6 and 8, so it reads on a light terminal
and a dark one without asking which: warm for the person, cool for the
tool and its machinery, dim for what happened elsewhere, red for what
failed. `Palette` names each of them (`Tool`, `Event`, `Result`,
`Rule`, `Needs`) and every one has a default.

On the rail, `SideItem.Needs` marks an item that is waiting on the
person. It cuts across the states rather than being one: a scout that
is done and whose report nobody has read is done, and it still wants
you, so it draws as `◆` and not as a tick.

The status line is the fixed facts on the left — name, model,
conversation, what the turn spent — and the application's own line on
the right. When the window is too narrow for both, the application's
text is cut before anything on the left, and the cost is the last
thing standing.

## Tools, policy, the ask

A tool is a name, a description, a JSON Schema and something to run:

```go
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, args json.RawMessage, h Handle) (Result, error)
}
```

A tool that takes paths says which arguments are paths (`Paths`); one
that runs a command says what the command line is (`Command`); one
whose verb is the question says what an "always" about it covers
(`Scope`). That is all a policy needs, and it is why a profile can
write rules about tools it has never heard of.

A `Handle` is what the harness lends a tool for one call: `Output` to
write what the person watches, `Say` for a line in the harness's own
voice before there is any result ("Opening a room…"), and `Values` for
what the harness knows that the arguments do not say — `tool.Device`
and `tool.Cwd` are the documented keys. The zero Handle works, so a
tool never checks: writing goes nowhere, saying says nothing, every
value is missing.

A `Result` is `Text` — what the model is told — and `Meta`, a small
JSON-shaped map the harness records and the model never sees: the task
a call started, the session it opened, what it cost (`tool.MetaTask`,
`MetaSession`, `MetaCost`).

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
res, err := t.Run(ctx, args, h)       // h.Output becomes tool_output events
```

Deciding touches no files. Every path is made absolute and cleaned
first, so a `../` cannot walk around a rule; an allow needs *every*
path of a call, a deny or an ask needs *one*; a command prefix matches
on a word boundary and never through a shell operator.

A rule can also key on an argument, which is how one tool with several
verbs gets several answers:

```toml
[[rules]]
tools = ["fleet"]
when  = { action = "stop" }
then  = "ask"
reason = "stopping a task abandons the work in it — check they meant to"
```

It is equality on top-level string arguments, every pair having to
match, and nothing cleverer: an argument that is a number, an object
or absent does not match, so a rule that cannot see what it asks about
does not fire.

Some tools are the harness's rather than the profile's — a supervisor
who cannot hand work away is not a supervisor, whatever her `tools:`
line forgot to say. `Registry.Own(tools...)` marks those, and `Only`
keeps them, first. `Replace` and `Remove` let a registry change while
it is being served from, which is what an MCP server saying its tool
list changed does.

The ask reaches the terminal as `agent.KindAsk` and comes back through
`agent.Answerer` — `yes`, `no`, or `always`. An `always` is remembered
by `tool.Gate` as a grant with a reach: what the tool said its `Scope`
was, or else the directory for a file and the program for a command.

## A profile

```
profiles/supervisor/
  profile.md     the system prompt, with name / model / tools on top
  policy.toml    the rules, in the order they are tried
  mcp.toml       MCP servers, if it has any
```

`profile.Load(dir)` returns the prompt, the tool names and the policy;
a typo in the rules is an error there rather than a surprise at
midnight. `profiles.Supervisor()` is the same directory, compiled in.

Read `GAPS.md` for what the loop of building it through Vera turned
up, and how each gap was classified.

## MCP

The third file in a profile is other people's tools:

```toml
[[servers]]
name    = "files"
command = "mcp-server-filesystem"
args    = ["~/notes"]
env     = { READ_ONLY = "1" }

[[servers]]
name    = "docs"
url     = "https://mcp.example.com/mcp"
headers = { Authorization = "Bearer ${DOCS_TOKEN}" }
```

A command is the stdio transport — a subprocess, talked to over its
stdin and stdout in newline-delimited JSON-RPC. A url is streamable
HTTP, POSTing and reading SSE back. `${VAR}` anywhere in a url, a
command, its args, an env value or a header comes from the
environment, so a token lives where a secret belongs and the file says
which one.

Two calls, and a harness has them:

```go
servers, err := mcp.Load(prof.Dir)             // nil if there is no mcp.toml
set, err := mcp.Connect(ctx, servers, reg)     // err names the ones that did not answer
defer set.Close()
```

Every tool every server offers is now in `reg` as `<server>.<tool>`,
with the server's own JSON Schema, as the registry's `Own` — a
`tools:` line written before the server existed does not drop them.
After that nothing about them is special: they are in
`Definitions()`, the policy decides them by name (a profile whose
default is ask asks the first time), and a `tools/call` happens in
`Run`. A `notifications/tools/list_changed` re-lists and makes the
registry agree while it is being served from.

Text comes back as text; an image, a sound or a resource becomes one
line saying what it was and how big, because the alternative is a
megabyte of base64 in a context window or a model told nothing
happened. A tool that says it failed is a `Result` saying so — the
model can work with "the disk is full" — and the protocol's `_meta`
becomes `Result.Meta`.

`mote mcp ls <profile>` connects and prints what came back, under the
names the model will see and a policy rule has to be written against.

The protocol is the official Go SDK,
`github.com/modelcontextprotocol/go-sdk`, which is at v1 and has both
transports; what is in `mcp` is what it has no opinion about. One
thing to know before a real model: a function name in an OpenAI
request body, and a tool name in an Anthropic one, must match
`[a-zA-Z0-9_-]{1,64}`, and a dot is not in it. `mcp.Separator` is what
goes between the server and the tool, and a harness that has met a
model sets it to `__` once, at startup, before `Connect`.

## Providers

The loop wants four things from a model, and they are one method:

```go
type Provider interface {
	Stream(ctx context.Context, req Request, fn func(Event)) (Usage, error)
}
```

A `Request` is a model, a system prompt, messages, the registry's
`tool.Definition`s and a token cap — plus three hints only some
providers honour: `Thinking` (off, or adaptive), `Effort`
(low…max) and `CacheSystem`. A provider with no opinion about one
ignores it rather than failing.

What arrives is `Event`s: a text delta, a thinking delta from a
provider that shows any, a tool call — complete, however many
fragments the wire cut its arguments into — or an error the model
made rather than one the socket did. `Usage` is what it cost: `Input`,
`Output`, `CacheRead`, `CacheWrite`, the model that actually answered
and why it stopped. The four counts do not overlap whichever wire
answered, which they do not on the wire: OpenAI's `prompt_tokens`
includes the cached ones and Anthropic's `input_tokens` does not, and
each provider corrects its own numbers on the way out.

`fn` is called on `Stream`'s goroutine, in order, and never after it
returns, so nothing that reads events needs a lock. Cancelling the
context ends the stream. A model that declined to answer is not an
error — that is a `KindError` event and a stop reason, because it
happened and it was paid for.

`provider.New` chooses, and a profile's `model:` line is enough:

```go
p, err := provider.New(provider.Config{Model: prof.Model})
```

A name starting with `claude`, and an Anthropic key to call it with,
gets the Messages API through the official SDK; anything else gets an
OpenAI-compatible `/chat/completions`. A claude model with no
Anthropic key is somebody's proxy, and going through it is the right
answer rather than an error about a key nobody meant to use.

Keys and endpoints come from the `Config`, or — for any field left
empty — from the environment:

| variable | what it is |
|---|---|
| `ANTHROPIC_API_KEY` | the key for the Messages API |
| `ANTHROPIC_BASE_URL` | somewhere other than the API itself |
| `OPENAI_API_KEY` | the key for the chat-completions endpoint |
| `OPENAI_BASE_URL` | that endpoint, if it is not OpenAI's |

An endpoint given without a key sends no `Authorization` header,
which is what a model running on this machine wants.

The Anthropic side asks for what the Messages API can do and nothing
it cannot: the system prompt goes as text blocks with an ephemeral
`cache_control` on the last one and another on the last tool, so the
stable prefix — tools, then prompt — is written once and read back on
every turn after; tool results that ran in parallel go back as
`tool_result` blocks in one user message; an assistant turn's kept
thinking blocks go back in front of its text and its tool calls, and
stay behind when the request turns thinking off; thinking is adaptive
by saying nothing at all, which is what a Claude 4.6 or 5 wants and is
why no `budget_tokens` is ever sent, and `ThinkingDisplay` becomes
`thinking.display`, which the API has a place for only on the adaptive
config — so asking for one asks for the other; `Effort` becomes
`output_config.effort`. The defaults are `claude-opus-5` and 64000
tokens, because `max_tokens` is required and streaming is what makes
a large one safe to ask for.

The OpenAI side has a place for one hint, `Effort`, and sends
`reasoning_effort` only when it was set. It used to send `"none"` for
`Thinking: off` — verad's way of telling an endpoint that refuses
function tools with reasoning on — and an OpenAI-compatible endpoint
answers that with `400: Unsupported value: 'reasoning_effort' does not
support 'none'` for a model that has no way to turn reasoning off. A
hint that fails the request is worse than a hint that was not taken.
`xhigh` and `max` become `high`, which is the strongest word that end
has; an `Effort` this package does not recognise is passed through as
the caller wrote it, so the endpoint that really does want `"none"`
asks for it by name.

Both are tested against `httptest` servers speaking their real
streaming formats, and one conformance test drives the same scripted
exchange — text, a tool call, a tool result, text — through both,
carrying each provider's own `Raw` back without reading it.

## Try it

```
go run ./cmd/mote demo        # the terminal, over a scripted agent
                              # /registers prints one of each
go run ./cmd/mote sessions    # the conversations it left behind
go run ./cmd/mote demo -c <id>  # reopen one
go run ./cmd/mote mcp ls <profile>  # its MCP servers, and their tools
```

In the demo, say a line with **policy** in it: eleven real tool calls
against this checkout, decided by `profiles/supervisor` — five
allowed, one denied ("start a task for that"), two that stop and ask,
a `delete` under her own home that does not, and two calls to `room`,
a tool the *harness* owns: one that the profile's `tools:` line never
named and cannot drop, that speaks in the harness's voice, hands back
a task id in `Result.Meta`, and is asked about only for the verb a
rule names. `/policy` prints the rules. The demo's `~` is a scratch directory it deletes when it
quits, so the writes are real and land nowhere real.

A call that does not run — denied, or asked about and refused — comes
back as `error: nothing was done: <why>` (`tool.Refused`,
`tool.Declined`), because a reason on its own reads like advice
beside a write that went through.

Conversations live under `$XDG_STATE_HOME/mote/sessions`, or
`~/.local/state/mote/sessions`.
