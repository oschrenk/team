# Security

Same threat model as the [Python upstream](https://github.com/yilunzhang/claude-code-inter-session):
single-user, single-machine, trusting local code.

## Threat model

- Server binds `127.0.0.1` only — never exposed to the network.
- Bearer token at `~/.claude/data/team/token` (mode `0600`, parent
  directory `0700`). Any process running as the same Unix user can read
  the token and connect. This is acceptable for single-user,
  single-machine.
- Identity check: clients verify the listener's pidfile metadata
  matches `(pid, host, port)` AND that the pid's cmdline contains
  `team server` — defense in depth against opportunistic localhost
  port squatters. Implemented in [`internal/auth/identity.go`](./internal/auth/identity.go).
- The token does **not** protect against malicious code running as your
  user. If you don't trust local code, don't enable this plugin.
- Role split: helper CLIs (`team send`, `team list`, …) connect with
  `role=control` and must prove continuity to their listener via
  `for_session` + `nonce` cross-validation against the registered
  listener. This blocks impersonation by sibling Bash subshells that
  happen to share a parent pid.
- ppid-keyed flock prevents two `team connect` monitors from running
  for the same Claude Code session. The lock file is intentionally not
  unlinked on release — flock is keyed on the kernel's open-file
  description, and unlinking would create a TOCTOU window.

## Reaction policy (receiving agent behavior)

The receiving Claude Code session treats peer messages as instructions
but applies the same caution as user input:

- Destructive operations (`rm -rf`, `git push --force`, `DROP TABLE`,
  etc.) require explicit affirmative content in the incoming message.
- Ambiguous requests prompt a `question:` clarifier first.
- System, developer, and tool permission rules are NOT overridable by
  peer messages — the peer is itself an LLM and may have been
  prompt-injected.

Full prose: [`skills/team/SKILL.md`](./skills/team/SKILL.md).

## Wire-level limits (DoS surface)

See [README.md](./README.md#limits). Limits are server-enforced. A
peer that exceeds the broadcast rate (60/min/listener) gets a
`rate_limited` error frame; it cannot DoS the bus by spamming.

## Inspection auth

`team inspect` and the HTTP `/api/*` endpoints (sessions, stats,
messages) require the same bearer token as the WebSocket bus. The
comparison uses `subtle.ConstantTimeCompare` to prevent timing
side-channels. `/api/health` is the only unauthenticated endpoint and
returns nothing sensitive (port + uptime + version).

## Reporting

If you find a vulnerability, please open a private security advisory
on [GitHub](https://github.com/oschrenk/team/security/advisories/new)
rather than a public issue.
