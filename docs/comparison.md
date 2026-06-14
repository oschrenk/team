# How `team` differs from the Python upstream

| Aspect            | Python `inter-session`                            | `team` (this)                                 |
| :---------------- | :------------------------------------------------ | :-------------------------------------------- |
| Platform          | macOS / Linux / WSL2                              | macOS only                                    |
| Distribution      | pip + venv re-exec                                | single static Go binary (brew)                |
| Server lifecycle  | Self-spawning via `bind()`-atomic election        | launchd user agent (`team service install`)   |
| Idle shutdown     | Configurable minutes                              | None (launchd `KeepAlive=true`)               |
| Plugin install-deps | Required first run                              | Not needed — single binary                    |
| Inspection        | Read `messages.log` manually                      | `team inspect` + `/api/*` HTTP endpoints      |

The wire protocol is identical — same JSON field names, op names, error
codes, and role split — so a Python client and a Go client could in
principle talk to the same bus. In practice you'd just run one or the
other.
