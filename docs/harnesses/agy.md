# Agy (Google Antigravity) Adapter

Agy runs in two AO modes:

- **Terminal UI:** AO launches the user's own `agy` CLI in the session worktree
  (`adapters/agent/agy`).
- **Chat:** AO launches Google's **Antigravity ACP server** and talks to it over
  the Agent Client Protocol (`adapters/chatdriver/agyacp`).

The Agy CLI and ACP server use the same native conversation UUID, but persist
the SQLite file under separate directories: the ACP server uses
`GEMINI_HOME/antigravity-acp/conversations`, while the CLI uses
`GEMINI_HOME/antigravity-cli/conversations`. After AO stops the source
controller, it copies the database and SQLite sidecars, metadata, and brain
files into the target directory. It resolves the per-session `GEMINI_HOME`
before falling back to the daemon environment or `~/.gemini`. The source
interface's database must exist: AO never falls back to an older copy in the
target directory, and a bridge failure fails the transition before the target
controller starts. Before offering a history handoff, AO checks the source
interface's database for persisted `steps`.

A terminal resumed by a Chat to Terminal switch has no fresh `Stop` hook until
its next turn, so AO reads the rendered screen to prove it is idle before
switching back: a `>` composer between two `─` rules with a `? for shortcuts`
footer. A draft, `esc to cancel` (working), or an approval or trust dialog is
not idle, and anything unrecognized fails the switch closed. The adapter does
not implement the raw-output `TerminalActivityDetector`, which would stall
message delivery to an Agy terminal.

The `agy` CLI has no ACP mode. Its machine interface is headless
`--output-format stream-json`, which cannot prompt for approvals. Google
publishes a separate ACP server, `agy_acp_server`, through the
[ACP registry](https://github.com/agentclientprotocol/registry) as
`antigravity-acp` (authors "Google LLC", proprietary license). Chat uses that
server.

## Install the ACP server (Chat mode)

Like Autohand's separately distributed `autohand-acp`, the Antigravity ACP
server is the user's own install. AO finds it and never downloads or bundles it.
Install it once from the registry entry for your platform
(`antigravity-acp/agent.json`, `distribution.binary.<platform>.archive`). For
example, on macOS Apple silicon, keeping both executables together and linking
the server beside `agy`:

```bash
mkdir -p ~/.local/share/antigravity-acp && cd ~/.local/share/antigravity-acp
curl -LO https://dl.google.com/agy-extensions/releases/macos/agy-acp-server-1.3.0-darwin-arm64.zip
unzip agy-acp-server-1.3.0-darwin-arm64.zip && rm agy-acp-server-1.3.0-darwin-arm64.zip
ln -sf ~/.local/share/antigravity-acp/agy_acp_server.par ~/.local/bin/agy_acp_server
```

AO resolves the server in this order:

1. `AO_AGY_ACP_SERVER_PATH`, an exact executable path. If it's set but missing,
   that's an error rather than a fallback.
2. `agy_acp_server` (or `agy_acp_server.par`) in the same directory as the
   resolved `agy` CLI.
3. `PATH`, then `/usr/local/bin`, `/opt/homebrew/bin` and `~/.local/bin`
   (`agy_acp_server.exe` on Windows).

The server needs its `localharness_external` helper. AO sets
`ANTIGRAVITY_HARNESS_PATH` to the helper beside the server's real location, so a
symlinked server works. AO admits only a server whose ACP handshake identifies
as `antigravity-acp`, version **1.3.0 or newer** (the version verified end to
end). On Linux, AO passes the registry's required `--uid=` argument.

## Sign-in

Sign in to Antigravity with Google first (for example, run `agy` once). The ACP
server keeps its settings and conversations under `GEMINI_HOME`
(default `~/.gemini`).

A fresh server has no auth method selected, and `session/new` answers
"Authentication required". When `GEMINI_HOME/google_accounts.json` records an
active account and `GEMINI_HOME/antigravity-acp/settings.json` has no
`auth.type`, AO calls ACP `authenticate` with `oauth-personal` once. That call
has a 20 s limit. The server reuses the existing sign-in and saves the choice to
its own `settings.json`. AO never calls `authenticate` when no sign-in is
recorded, because ACP Agent Auth can open a browser. Users with Gemini
Enterprise or an API key should set `auth.type` themselves.

## Approvals

Antigravity raises `session/request_permission` for every gated tool call, and
AO shows each one as an approval card. Approval modes map live onto
Antigravity's modes:

| AO approval | Antigravity mode |
|---|---|
| default | `default` (asks for gated tools) |
| accept-edits | `auto_edit` (auto-approves file edits) |
| auto | `yolo` (Antigravity has no classifier mode, so auto runs without asking) |
| bypass | `yolo` (auto-approves all tools) |

AO never answers a request with `allow_always` on the user's behalf. Antigravity
caches that grant, which would make a later switch to a stricter mode ineffective.

## Standing instructions

Antigravity accepts no system-prompt flag. AO sends its standing instructions as
a text block ahead of the first prompt of a new session. A resumed conversation
that already has turns, in AO's own history (including a live reconnect) or in
the `session/load` replay, received them then, so they are not repeated.
Instructions sent as an embedded resource were treated as something to look up
rather than as instructions.

## Known limitations

- AO prepares Chat worktrees the same way as Terminal ones, provisioning
  workspace hooks (`.agents/hooks.json`). AO sets `AGY_ACP_DISABLE_WORKSPACE_TRUST=1`
  in the server launch environment so `agy_acp_server` runs without prompting
  for workspace hook trust.
- Reattaching to a still-running provider after the daemon detaches
  (`Close` then `Resume`) left `session/set_mode` unanswered in testing.
  Resuming after the provider exits (`session/load`) works and is covered by the
  live test.
- On the first turn Antigravity may try to list its own skills directory
  (`$GEMINI_HOME/antigravity-cli/skills`), which appears as an approval request.
- Upstream reports to watch: OAuth sign-in hangs (zed#63464), Linux sign-in and
  startup crashes (zed#64114, registry#607, registry#547), Windows temp-dir and
  orphan-process leaks (zed#64479), and slow initialization (registry#595).

## Verification

`backend/internal/adapters/chatdriver/agyacp/live_test.go` is the registration
gate. Run it with `AO_LIVE_AGY=1` (and `AO_AGY_ACP_SERVER_PATH` if the server
isn't in the managed location). It uses a private `GEMINI_HOME`, so it never
writes the operator's `~/.gemini`. It checks:

- standing instructions;
- Reject blocks a tool;
- yolo runs it;
- a switch back to default asks again;
- interrupt, then continue;
- resume with context after close.
