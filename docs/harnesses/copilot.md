# GitHub Copilot Adapter

GitHub Copilot runs in two AO modes:

- **Terminal UI:** AO launches the user's own `copilot` CLI in the session
  worktree (`adapters/agent/copilot`).
- **Chat:** AO launches the same binary as `copilot --acp` and talks to it over
  the Agent Client Protocol (`adapters/chatdriver/copilotacp`).

Both modes use the binary, sign-in, entitlement, models, and MCP servers of the
user's own installation. AO never downloads or bundles Copilot.

Everything below was verified live against Copilot CLI **1.0.93** on
2026-10-13. Run `AO_LIVE_COPILOT_ACP=1 go test ./internal/adapters/chatdriver/copilotacp/ -run Live`
to repeat it against your own account. The live tests use premium requests.

## Requirements

AO admits only an ACP server whose handshake identifies as `Copilot`, version
1.0.93 or newer. It reads the version from the handshake rather than running
`copilot --version`.

Sign in with `copilot login` (or `gh auth login`, or a BYOK provider). The Chat
probe reuses the terminal adapter's auth check.

## Handshake

`initialize` reports protocol version 1, `loadSession: true`, image and
embedded-context prompts, HTTP and SSE MCP servers, and `session/list` and
`session/close`. `session/new` advertises three config options:

| Option | Values | How AO uses it |
|---|---|---|
| `mode` | Agent, Plan, Autopilot | Agent and Plan appear as the composer's Plan Mode toggle. Autopilot is hidden and refused (see Approvals). |
| `agent` | `""` plus each `.github/agents` profile | AO selects its per-session profile (see Standing instructions). Hidden from the composer. |
| `allow_all` | `on`, `off` | Driven by AO's approval mode. Hidden from the composer and refused as a direct setting. |

## Standing instructions

Copilot has no system-prompt flag, and **`--agent` is ignored under `--acp`**:
the flag parses, but the session still reports the default agent. Instead,
Copilot reads `.github/agents` once at process start and offers each profile as
a value of the `agent` option.

So Chat writes the same `ao-<session>.agent.md` profile as the terminal launch
(git-excluded) before starting Copilot, then selects it through
`session/set_config_option` once the session exists. The choice persists across
`session/load`, and AO selects it again after a resume anyway.

## Approvals

The launch flags fix a baseline for the whole process. Gated tools then raise
`session/request_permission`, which AO shows as an approval card, and a
rejection is honored.

| AO approval | Launch | Verified behavior |
|---|---|---|
| default | no flag | Shell commands ask. A rejection leaves the file untouched. |
| accept-edits | `--allow-tool write` | File edits run without asking. Shell commands still ask. |
| auto | `--allow-all-tools` | Shell commands run without asking. |
| bypass | no flag, then `allow_all=on` | Everything runs without asking. |

`allow_all` is the only approval control Copilot can change on a live process.
Turning it off returns the session to its launch baseline. So a Chat session
can switch between its baseline and bypass, and any other change fails with
"restart Chat to use …" instead of quietly running under a different policy.

Bypass deliberately does **not** launch with `--allow-all`, which the terminal
adapter uses. A process started with `--allow-all` keeps running every tool
without asking even after `allow_all` is turned off. Starting at the default
baseline and turning `allow_all` on lets a later switch back to default really
ask again.

Copilot's **Autopilot** mode turns allow-all on inside Copilot without AO
knowing, so the composer never offers it and the daemon refuses it as a direct
setting. Choose bypass permissions instead.

AO never answers an approval with `allow_always` on the user's behalf.

## Models and effort

Under `--acp`, Copilot ignores both `--model` and `COPILOT_MODEL`. The model can
change only through a `model` config option, and only if the session advertises
one. 1.0.93 acknowledges a `model` option it never advertised without applying
it, so the binding requires advertised options: a model the session does not
offer fails with a clear error instead of silently running another model.

The account used for verification is limited to Copilot's Auto model (the
terminal CLI rejects every named model on it as "not available"), and its ACP
sessions advertise no `model` option. On such an account Chat runs Auto and
shows no model picker. Whether accounts with model access advertise a `model`
option over ACP has **not** been verified.

Copilot advertises no reasoning-effort option over ACP. AO does not send an
effort, and an effort carried over from another harness does not fail the
session.

## Resume and interface switching

Chat resumes through `session/load`. Copilot replays the conversation, and
follow-up turns keep its context.

Terminal and Chat share Copilot's session store (`~/.copilot/session-state`,
or `COPILOT_HOME`): an ACP `session/load` opens a terminal session, and
`copilot --resume` continues a Chat session. The terminal adapter declares this
shared identity, so a session can switch interfaces with its history. Copilot
creates a session's directory before its first turn but cannot load it until a
message is recorded, so a session with no recorded user message starts fresh.

Switching back to Chat resumes the session and then reloads it to pick up
the turns made in the terminal. Copilot answers a second `session/load` on the
same connection with "Session … is already loaded", so the binding closes the
session (`session/close`) before each reload. Its config options, including the
selected agent profile and `allow_all`, survive the close.

Copilot's replay carries no message ids. AO splits the replay into turns at
each user prompt that follows an answer, so every prompt keeps its own reply and
AO can match the replay against the turns it already recorded.

Only one Copilot process may hold a session. Loading a session that another live
Copilot process still owns leaves the next prompt waiting with no output.

## Interrupt

`session/cancel` stops a running turn, which ends with stop reason `cancelled`.
