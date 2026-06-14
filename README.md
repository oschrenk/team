# team

Agent-to-agent messaging for Claude Code sessions.

Each Claude Code session connects to a local WebSocket bus and can send messages to other connected sessions; incoming messages are delivered to the receiving agent as prompts and **acted on as instructions by default**. One session can drive another.

Go port of [`claude-code-inter-session`](https://github.com/yilunzhang/claude-code-inter-session) (see [docs/comparison.md](./docs/comparison.md)).

## Quickstart

```
brew install oschrenk/made/team
team service install
```

Then in any Claude Code session:

```
/plugin marketplace add https://github.com/oschrenk/team
/plugin install team@team
/team:team connect
```

`team service install` is a one-time machine setup that drops a launchd
user agent at `~/Library/LaunchAgents/com.oschrenk.team.plist` and
bootstraps it. The bus then runs in the background until you
`team service uninstall`.

Standalone skill install (cleaner `/team` prefix, no `team:team` namespace)
and other install options live in [docs/install.md](./docs/install.md).

## Subcommands

| Command                                  | What it does                                                         |
| :--------------------------------------- | :------------------------------------------------------------------- |
| `team service install`                   | Set up the launchd user agent (one-time per machine).                |
| `team service uninstall`                 | Remove the launchd user agent.                                       |
| `team service status`                    | Show the service state (loaded / running / pid).                     |
| `team service start` / `stop` / `restart`| Lifecycle control via `launchctl`.                                   |
| `team service logs`                      | `tail -F` the server log at `~/Library/Logs/team/server.log`.        |
| `team connect [name]`                    | Long-lived monitor (the Claude Code Monitor entry point).            |
| `team list`                              | List connected sessions.                                             |
| `team send <name> <text>`                | Send a message to one session.                                       |
| `team broadcast <text>`                  | Send to all other sessions (≤ 256 KB).                               |
| `team rename <new-name>`                 | Rename the listener.                                                 |
| `team status`                            | Show this session's connection state.                                |
| `team disconnect`                        | Print the listener's pid + how to stop it.                           |
| `team inspect`                           | Live snapshot of the bus (sessions, stats, recent messages).         |
| `team server`                            | Run the bus in the foreground (used by the launchd agent).           |

## Inspect — the headline feature

The bus exposes a small JSON API on the same listener, behind the same
bearer token, plus a `team inspect` CLI that pretty-prints it:

```
$ team inspect
server: alive @ 127.0.0.1:9473 (uptime 5s, version 0.1.0)

sessions (2 connected):
  NAME   SESSION   CWD                                SINCE
  alice  b1d4b424  /Users/oliver/Projects/tools/team  01:13:54
  bob    5e384b0c  —                                  01:13:55

stats: msgs sent=2 (broadcast=1, rejected=0)  peers joined/left: 2/0

recent messages (3):
  [01:13:56] alice → bob: hi bob #1
  [01:13:57] alice → bob: hi bob #2
  [01:13:58] alice → (all): hello everyone
```

Raw JSON via `team inspect --json | jq .`. Live updates via
`team inspect --watch`. Last N messages via `-n N`.

The underlying HTTP API:

| Route                  | Auth   | What it returns                                  |
| :--------------------- | :----- | :----------------------------------------------- |
| `GET /api/health`      | none   | `{ok, port, uptime_s, version}`                  |
| `GET /api/sessions`    | bearer | current registry snapshot                        |
| `GET /api/stats`       | bearer | counters + uptime                                |
| `GET /api/messages?n=N`| bearer | tail of last N records from `messages.log` (≤500)|

Auth via `Authorization: Bearer <token>` where the token lives at
`~/.claude/data/team/token` (0600).

## Limits

| Limit                          | Value                                       |
| :----------------------------- | :------------------------------------------ |
| WebSocket frame                | 16 MB                                       |
| Direct `text` length           | 10 MB (server-enforced)                     |
| Broadcast `text` length        | 256 KB (server-enforced)                    |
| Stdout notification body       | 400 codepoints (long messages get a `cont` pointer to `messages.log`) |
| Broadcast rate                 | 60 / minute / listener                      |

Direct messages whose body exceeds the stdout cap display as a truncated
first-line plus a `cont` line pointing to `messages.log`. The full
payload is always preserved in `messages.log` regardless of stdout
truncation.

## Development

See [DEVELOPMENT.md](./DEVELOPMENT.md).

## License

MIT — see [LICENSE](./LICENSE).
