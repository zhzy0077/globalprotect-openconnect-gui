# gpclient-gui

A Go/Fyne GUI for GlobalProtect VPN using `gpclient` and `gpauth` as backends.
It is completely independent of the existing codebase and does not modify any
installed binaries.

## How it works

1. **First connect** — opens your browser via `gpauth`, captures the SAML auth
   cookie, saves it to `~/.config/gpclient-gui/auth.json`, then launches
   `sudo gpclient connect <portal> --cookie-on-stdin`.

2. **Reconnect** — reuses the cached `portalUserauthcookie` to skip the browser.
   If the server rejects it (`auth-failed`), the cache is cleared and the
   browser reopens automatically.

3. **Disconnect** — sends `SIGINT` to the gpclient process (via the PID in
   `/var/run/gpclient.lock`), triggering a clean logout and tunnel teardown.

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| Go 1.21+ | `sudo apt install golang-go` or download from golang.org |
| `gpauth` / `gpclient` | From the `globalprotect-openconnect` package |
| `sudo gpclient` without password | Add to `/etc/sudoers.d/` (see below) |
| Fyne system dependencies | See below |

### Fyne build dependencies (one-time)

```bash
sudo apt install \
  libgl1-mesa-dev \
  xorg-dev \
  libayatana-appindicator3-dev
```

### Passwordless sudo for gpclient

Create `/etc/sudoers.d/gpclient`:

```
%sudo ALL=(ALL) NOPASSWD: /usr/bin/gpclient
```

## Build

```bash
make build
```

## Run

```bash
./gpclient-gui
```

The app starts as a system tray icon (grey circle = disconnected).
Right-click the tray icon or left-click to open the status window.

On first run, click the ⚙ settings button and enter your portal hostname
(e.g. `vpn.mycompany.io`).

## File locations

| File | Purpose |
|------|---------|
| `~/.config/gpclient-gui/config.json` | Portal URL and browser preference |
| `~/.config/gpclient-gui/auth.json`   | Cached `portalUserauthcookie` (mode 0600) |
