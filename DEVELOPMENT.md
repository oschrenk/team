# Development

**Requirements**

- [nix](https://nixos.org/) with flakes enabled (provides `go`, `gopls`, `golangci-lint`, `go-task`)
- [direnv](https://direnv.net/) (optional) — `direnv allow` to auto-load the flake shell

Without direnv, drop into the shell with `nix develop`.

You'll also want `staticcheck` for `task check`:

```
go install honnef.co/go/tools/cmd/staticcheck@latest
```

## Commands

- `task build` — Build project (`./team`)
- `task run` — `go run main.go`
- `task test` — `go test ./...`
- `task tidy` — `go mod tidy`
- `task check` — `staticcheck ./...`
- `task lint` — `check` + `tidy`
- `task install` — Install to `$GOBIN/`
- `task uninstall` — Remove from `$GOBIN/`
- `task artifacts` — Build release artifacts for darwin arm64 + amd64 in `./.release`
- `task sha` — Print sha256 of release artifacts
- `task tag` — Push git tag from `VERSION`
- `task release` — Tag + create GitHub release with artifacts
- `task smoke` — Run the end-to-end smoke test (server + connect + helpers + inspect)
- `task clean` — Remove `./team` and `./.release`
- `task updates` — Find dependency updates

## Architecture

See [SPEC.md](./SPEC.md) for the overall design. Implementation tickets
live in [`tasks/`](./tasks/) and are tracked in [TODO.md](./TODO.md).

## Release

1. Bump version in `VERSION` (plain text, e.g. `0.2.0`).
2. Bump version in **both** `.claude-plugin/plugin.json` and
   `.claude-plugin/marketplace.json` to match — they must stay in sync
   (the marketplace listing and the installed-plugin update detection
   are different code paths).
3. Commit the version bumps.
4. `task release` — builds artifacts, signs (if configured), pushes
   tag, creates GitHub release with `gh release create`.
5. `task sha` — prints sha256 of each artifact for the brew formula.
6. Update [`oschrenk/homebrew-made`](https://github.com/oschrenk/homebrew-made)
   with the new version + shas. See `.release/team.rb.example` for a
   starting formula.
7. `brew update && brew upgrade team` to verify the bottle works.

## End-to-end smoke

`task smoke` spins up `team server` on a free port, starts two `team
connect` listeners, exchanges messages between them, and exercises
`team inspect`. Useful before tagging a release.
