# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build      # go mod tidy + go build -o gpoc-gui .
make run        # build then run the binary
make clean      # remove the binary
make deps       # go mod tidy only
make install    # install binary to /usr/local/bin/ and set up sudoers rule

go test ./...                        # run all tests
go test ./internal/vpn/...           # run tests for a single package
go vet ./...                         # static analysis
```

Build requires Fyne system libraries: `libgl1-mesa-dev`, `xorg-dev`, `libayatana-appindicator3-dev`.

## Architecture

The app owns the GlobalProtect portal HTTP flow natively (mirroring `gpclient` behavior) and delegates only tunnel management to `openconnect` via a subprocess. SAML browser auth is handled by the external `gpauth` binary.

### Package structure

| Package | Purpose |
|---------|---------|
| `internal/ui` | Fyne GUI, system tray, settings dialog, gateway selection |
| `internal/vpn` | `openconnect` subprocess lifecycle and state machine |
| `internal/portal` | HTTP calls to GlobalProtect portal/gateway endpoints |
| `internal/auth` | `gpauth` binary invocation and credential caching |
| `internal/config` | `~/.config/gpoc-gui/config.json` persistence |
| `internal/credential` | Credential types (Prelogin, AuthCookie, Password) |
| `internal/errors` | Portal-specific error types for fallback handling |
| `assets` | Embedded PNG icons for tray states |

### State machine

`internal/vpn/manager.go` defines 6 states and drives transitions by parsing `openconnect` log output line-by-line:

| Log pattern | State transition |
|-------------|------------------|
| `"Connected as "` or `"Configured as "` | → **Connected** |
| `"GlobalProtect gateway refused"` or `"auth-failed"` | → **AuthFailed** |
| `SIGTERM`/`SIGINT` received | → **Disconnecting** |
| Process EOF (was connected) | → **Disconnected** |
| Process EOF (never connected) | → **Error** |

State changes are sent on `chan vpnStateMsg` (`stateCh`) to the UI goroutine in `internal/ui/app.go`. **Only the UI goroutine mutates Fyne widgets** (in `applyState`).

### Connection flow

1. Check for cached credentials in `~/.config/gpoc-gui/auth.json` (`auth.LoadCredentials`).
2. If `PortalCookieFromConfig` and `GatewayAddress` exist, attempt **seamless reconnect** (skips `portal.GetConfig`):
   - `portal.GatewayLogin` with cached cookies → URL-encoded token
   - `mgr.Connect(gateway, token)` launches `sudo openconnect --protocol=gp --cookie-on-stdin`
3. On cache miss or gateway login failure: perform **fresh authentication**:
   - `auth.RunGpauth` launches browser for SAML flow
   - `portal.GetConfig` fetches gateway list from portal
   - If multiple gateways: show selection dialog (saved to config)
   - `portal.GatewayLogin` exchanges portal cookie for openconnect token
   - `mgr.Connect` launches openconnect
4. On `AuthFailed` state: clear cache (`auth.ClearCredentials`) and repeat from step 3.

### Gateway selection

If the portal returns multiple gateways and no specific gateway is configured, a Fyne dialog prompts the user to select one. The selected gateway is persisted to `config.json` for subsequent connections.

### Disconnect

Sends `SIGTERM` to the openconnect process by reading its PID from `/var/run/openconnect.lock`. Falls back to interrupting the subprocess directly if the PID file is missing.

### Sudoers rule

```
%sudo ALL=(ALL) NOPASSWD: /usr/sbin/openconnect, /usr/bin/kill
```

Install with `sudo make install` or run `scripts/install-sudoers.sh` as root.

### Config & credential files

| Path | Contents |
|------|----------|
| `~/.config/gpoc-gui/config.json` | `Portal`, `Gateway`, `Browser` |
| `~/.config/gpoc-gui/auth.json` | Cached SAML data, portal cookies, gateway info, timestamp (mode 0600) |

### Tray icons

Tray icons are embedded PNG assets (`assets/vpn-*.png`) loaded via `//go:embed`. Three states: grey (disconnected), amber (connecting/disconnecting), green (connected).
