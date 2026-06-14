# Install

Two ways to expose `team` to Claude Code. Both work; pick based on what
prefix you want when invoking it.

The **machine setup** is the same either way:

```
brew install oschrenk/made/team
team service install    # writes ~/Library/LaunchAgents/com.oschrenk.team.plist
```

What differs is how Claude Code finds the skill.

## Plugin install

Inside any Claude Code session:

```
/plugin marketplace add /Users/oliver/Projects/tools/team
/plugin install team@team
```

(Or `/plugin marketplace add <git-url>` if installing from a clone of a
remote repo.)

**Invocation:** `/team:team connect`, `/team:team list`, etc.

**You get:**

- `userConfig` — change `port` via `/plugin config` if you ever need a
  non-default port
- Updates via `/plugin update team@team` after `git pull`

**The `team:team` namespace** is `<plugin-name>:<skill-name>`. Both are
`team`, hence the doubled prefix.

## Standalone skill install

Skip the plugin layer entirely — symlink the skill directory into
Claude Code's skills dir:

```
ln -s /Users/oliver/Projects/tools/team/skills/team "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills/team"
```

Or copy if you don't want the symlink:

```
cp -r /Users/oliver/Projects/tools/team/skills/team "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills/team"
```

**Invocation:** `/team connect`, `/team list`, etc. (no namespace prefix).

**Trade-offs:**

- No `userConfig` port knob → for a non-default port, set `TEAM_PORT` in
  your shell env
- No `/plugin update` flow → updates are `git pull` in the source repo
  (the symlink keeps tracking it)

For personal single-machine use with the default port `9473`, the
standalone mode loses essentially nothing and gives a cleaner prefix.

### Multiple Claude Code profiles

If you run more than one CC profile via `CLAUDE_CONFIG_DIR` (e.g. one for
personal work and one for $WORK), each profile has its own skills
directory and needs its own symlink. Run the same `ln -s` command once
per profile with the appropriate `CLAUDE_CONFIG_DIR` exported:

```
CLAUDE_CONFIG_DIR=$HOME/.config/claude/personal \
  ln -s /Users/oliver/Projects/tools/team/skills/team \
        "$CLAUDE_CONFIG_DIR/skills/team"

CLAUDE_CONFIG_DIR=$HOME/.config/claude/work \
  ln -s /Users/oliver/Projects/tools/team/skills/team \
        "$CLAUDE_CONFIG_DIR/skills/team"
```

Both symlinks point at the same source-of-truth, so `git pull` updates
both at once.

Both profiles share **one bus** (the launchd-managed server uses a
machine-wide data dir at `~/.claude/data/team/`, independent of
`CLAUDE_CONFIG_DIR`). A `team list` from either profile sees sessions
from both; `team send` and `team broadcast` cross profile boundaries.
Use the `CWD` column to tell sessions apart by where they're running.

If cross-profile noise turns out to bother you in practice, scoping
mechanisms (per-profile or per-cwd filters) are easy to add later.

## Switching modes

If you've installed via plugin and want to switch to standalone:

```
# in Claude Code:
/plugin uninstall team@team

# in your shell:
ln -s /Users/oliver/Projects/tools/team/skills/team "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills/team"
```

Or the other direction:

```
rm "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills/team"
# in Claude Code:
/plugin marketplace add /Users/oliver/Projects/tools/team
/plugin install team@team
```

## Verification

In a fresh CC session:

```
/team[:team] connect
```

You should see something like:

```
Connecting as `team`…
```

If you see `[team] server not running`, you missed `team service install`
during machine setup — run it now and reconnect.
