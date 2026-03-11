# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build      # go mod tidy + go build -o gpclient-gui .
make run        # build then run the binary
make clean      # remove the binary
make deps       # go mod tidy only

go test ./...                        # run all tests
go test ./internal/vpn/...           # run tests for a single package
go vet ./...                         # static analysis
```

Build requires Fyne system libraries: `libgl1-mesa-dev`, `xorg-dev`, `libayatana-appindicator3-dev`.

## Architecture

The app is a thin orchestration layer — it owns no VPN logic itself. All VPN work is delegated to two external binaries (`gpauth`, `gpclient`) via subprocesses.

### State machine

`internal/vpn/manager.go` defines 6 states and drives transitions by parsing `gpclient` log output line-by-line:

- `"Wrote PID"` → **Connected**
- `"auth-failed"` → **AuthFailed**
- process EOF → **Disconnected** or **Error**

State changes are sent on a `chan vpn.State` (`stateCh`) to the UI goroutine, which is the only place Fyne widgets are mutated (`applyState` in `internal/ui/app.go`).

### Connection flow

1. Load `~/.config/gpclient-gui/auth.json` (cached `portalUserauthcookie`).
2. If present, attempt reconnect by passing `portalUserauthcookie` as the `preloginCookie` field in the JSON sent to `gpclient`.
3. On `AuthFailed`: clear cache, run `gpauth` subprocess, wait for its JSON output, save new credentials, retry.
4. Credentials are piped to `sudo gpclient connect <portal> --cookie-on-stdin` via stdin (with a 300 ms delay to avoid a race on stdin readiness).

### Disconnect

Reads the gpclient PID from `/var/run/gpclient.lock` and sends `SIGINT`. Falls back to interrupting the sudo child process if the lock file is unavailable.

### Config & credential files

| Path | Contents |
|------|----------|
| `~/.config/gpclient-gui/config.json` | `Portal` (hostname) and `Browser` string |
| `~/.config/gpclient-gui/auth.json` | `CachedAuth`: `SamlAuthData` + timestamp, mode 0600 |

### Icon generation

`internal/ui/png.go` generates tray icons at runtime (filled circle PNGs) — grey/amber/green for disconnected/connecting/connected. There are no static image assets for these icons.
