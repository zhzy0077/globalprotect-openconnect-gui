// Package ui wires together the Fyne GUI, system tray, auth and VPN layers.
package ui

import (
	"context"
	"fmt"
	"image/color"
	"os/exec"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/gpclient-gui/gpclient-gui/internal/auth"
	"github.com/gpclient-gui/gpclient-gui/internal/config"
	"github.com/gpclient-gui/gpclient-gui/internal/portal"
	"github.com/gpclient-gui/gpclient-gui/internal/vpn"
)

// App is the top-level application object.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	cfg     *config.Config
	mgr     *vpn.Manager

	// live-update widgets
	statusDot    *canvas.Circle
	statusLabel  *widget.Label
	gatewayLabel *widget.Label
	connectBtn   *widget.Button
	portalLabel  *widget.Label

	// tray menu items kept for enable/disable updates
	trayMenu       *fyne.Menu
	trayConnect    *fyne.MenuItem
	trayDisconnect *fyne.MenuItem

	// channel used to route VPN state changes back to the Fyne event loop
	stateCh chan vpnStateMsg

	authCtx    context.Context
	authCancel context.CancelFunc
}

type vpnStateMsg struct {
	state   vpn.State
	gateway string
}

// NewApp constructs the application.  Call Run() to start the event loop.
func NewApp() *App {
	a := &App{
		fyneApp: app.NewWithID("io.github.gpclient-gui"),
		stateCh: make(chan vpnStateMsg, 8),
	}

	var err error
	a.cfg, err = config.Load()
	if err != nil {
		a.cfg = &config.Config{Browser: "default"}
	}

	a.mgr = vpn.New(func(s vpn.State, gw string) {
		a.stateCh <- vpnStateMsg{state: s, gateway: gw}
	})

	a.buildWindow()
	a.setupTray()
	return a
}

// Shutdown disconnects the VPN and quits the Fyne app.
func (a *App) Shutdown() {
	a.mgr.Disconnect()
	a.fyneApp.Quit()
}

// Run starts the Fyne event loop (blocks until quit).
// Fyne v2 is thread-safe for widget updates, so we drive UI changes from
// a background goroutine that drains the state channel.
func (a *App) Run() {
	go func() {
		for msg := range a.stateCh {
			a.applyState(msg.state, msg.gateway)
		}
	}()

	a.fyneApp.Run()

	// App is exiting — disconnect VPN if still connected.
	a.mgr.Disconnect()
}

// ---- main window ------------------------------------------------------------

func (a *App) buildWindow() {
	a.window = a.fyneApp.NewWindow("GlobalProtect VPN")
	a.window.SetCloseIntercept(func() { a.window.Hide() })

	a.statusDot = canvas.NewCircle(colorGrey)
	a.statusDot.Resize(fyne.NewSize(14, 14))

	a.statusLabel = widget.NewLabel("Disconnected")
	a.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	a.gatewayLabel = widget.NewLabel("")
	a.gatewayLabel.Hide()

	a.portalLabel = widget.NewLabel(a.portalDisplay())
	a.portalLabel.Alignment = fyne.TextAlignCenter

	a.connectBtn = widget.NewButton("Connect", a.onConnectPressed)
	a.connectBtn.Importance = widget.HighImportance

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), a.showSettings)
	settingsBtn.Importance = widget.LowImportance

	statusRow := container.NewHBox(a.statusDot, a.statusLabel)

	content := container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(a.portalLabel),
		container.NewCenter(statusRow),
		container.NewCenter(a.gatewayLabel),
		layout.NewSpacer(),
		container.NewCenter(a.connectBtn),
		layout.NewSpacer(),
		container.NewBorder(nil, nil, nil, settingsBtn),
	)

	a.window.SetContent(container.NewPadded(content))
	a.window.Resize(fyne.NewSize(300, 260))
	a.window.SetFixedSize(true)
	a.window.CenterOnScreen()
}

func (a *App) portalDisplay() string {
	if a.cfg != nil && a.cfg.Portal != "" {
		return a.cfg.Portal
	}
	return "No portal configured"
}

// ---- system tray ------------------------------------------------------------

func (a *App) setupTray() {
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		return
	}

	a.trayConnect = fyne.NewMenuItem("Connect", a.onConnectPressed)
	a.trayDisconnect = fyne.NewMenuItem("Disconnect", func() { a.mgr.Disconnect() })
	a.trayDisconnect.Disabled = true

	showItem := fyne.NewMenuItem("Show", func() {
		a.window.Show()
		a.window.RequestFocus()
	})

	menu := fyne.NewMenu("GlobalProtect VPN",
		a.trayConnect,
		a.trayDisconnect,
		fyne.NewMenuItemSeparator(),
		showItem,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", a.Shutdown),
	)

	a.trayMenu = menu
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(trayIcon(colorGrey))
}

func (a *App) updateTray(s vpn.State) {
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		return
	}
	busy := s == vpn.StateConnecting || s == vpn.StateDisconnecting
	a.trayConnect.Disabled = s == vpn.StateConnected || busy
	a.trayDisconnect.Disabled = s == vpn.StateDisconnected || s == vpn.StateAuthFailed || s == vpn.StateError
	a.trayMenu.Refresh()

	switch s {
	case vpn.StateConnected:
		desk.SetSystemTrayIcon(trayIcon(colorGreen))
	case vpn.StateConnecting, vpn.StateDisconnecting:
		desk.SetSystemTrayIcon(trayIcon(colorAmber))
	default:
		desk.SetSystemTrayIcon(trayIcon(colorGrey))
	}
}

// ---- state machine ----------------------------------------------------------

func (a *App) applyState(s vpn.State, gateway string) {
	a.updateTray(s)

	switch s {
	case vpn.StateDisconnected:
		a.statusDot.FillColor = colorGrey
		a.statusLabel.SetText("Disconnected")
		a.gatewayLabel.Hide()
		a.connectBtn.SetText("Connect")
		a.connectBtn.Enable()

	case vpn.StateConnecting:
		a.statusDot.FillColor = colorAmber
		a.statusLabel.SetText("Connecting…")
		if gateway != "" {
			a.gatewayLabel.SetText(fmt.Sprintf("Gateway: %s", gateway))
			a.gatewayLabel.Show()
		}
		a.connectBtn.SetText("Connecting…")
		a.connectBtn.Disable()

	case vpn.StateConnected:
		a.statusDot.FillColor = colorGreen
		a.statusLabel.SetText("Connected")
		if gateway != "" {
			a.gatewayLabel.SetText(fmt.Sprintf("Gateway: %s", gateway))
			a.gatewayLabel.Show()
		}
		a.connectBtn.SetText("Disconnect")
		a.connectBtn.Enable()

	case vpn.StateDisconnecting:
		a.statusDot.FillColor = colorAmber
		a.statusLabel.SetText("Disconnecting…")
		a.connectBtn.SetText("Disconnecting…")
		a.connectBtn.Disable()

	case vpn.StateAuthFailed:
		// Cache is stale — clear it and kick off a fresh browser auth.
		a.statusDot.FillColor = colorGrey
		a.statusLabel.SetText("Re-authenticating…")
		a.connectBtn.SetText("Connecting…")
		a.connectBtn.Disable()
		auth.ClearCredentials()
		go a.connectFresh()

	case vpn.StateError:
		a.statusDot.FillColor = colorGrey
		a.statusLabel.SetText("Error")
		a.connectBtn.SetText("Retry")
		a.connectBtn.Enable()
	}

	a.statusDot.Refresh()
}

// ---- connect / disconnect actions -------------------------------------------

func (a *App) onConnectPressed() {
	switch a.mgr.State() {
	case vpn.StateConnected, vpn.StateConnecting:
		a.mgr.Disconnect()
	default:
		go a.doConnect()
	}
}

// checkSudoRule returns an error if the sudoers NOPASSWD rule for openconnect
// is not in place. Uses sudo -n -l to avoid any password prompt.
func checkSudoRule() error {
	cmd := exec.Command("sudo", "-n", "-l", "/usr/sbin/openconnect")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"Missing sudoers rule for openconnect.\n\n" +
				"Run the following to fix it:\n\n" +
				"  sudo make install\n\n" +
				"Or manually:\n" +
				"  echo '%%sudo ALL=(ALL) NOPASSWD: /usr/sbin/openconnect, /usr/bin/kill' \\\n" +
				"    | sudo tee /etc/sudoers.d/gpclient-gui\n" +
				"  sudo chmod 0440 /etc/sudoers.d/gpclient-gui",
		)
	}
	return nil
}

func (a *App) doConnect() {
	if err := checkSudoRule(); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	if a.cfg.Portal == "" {
		a.showSettings()
		return
	}

	if a.authCancel != nil {
		a.authCancel()
	}
	a.authCtx, a.authCancel = context.WithCancel(context.Background())

	// Attempt seamless reconnect using the cached portal cookie + gateway.
	if cached, err := auth.LoadCredentials(); err == nil &&
		cached.PortalCookieFromConfig != "" && cached.GatewayAddress != "" {
		go a.tryReconnect(cached)
		return
	}

	// No usable cache → browser-based first authentication.
	go a.connectFresh()
}

// tryReconnect skips the portal getconfig.esp call entirely and goes straight
// to the gateway's login.esp using the cached portal cookie.  Falls back to
// connectFresh on any error.
func (a *App) tryReconnect(cached *auth.CachedAuth) {
	token, err := portal.GatewayLogin(
		cached.GatewayAddress,
		cached.Username,
		cached.PortalCookieFromConfig,
		cached.PrelogonCookieFromConfig,
	)
	if err != nil {
		auth.ClearCredentials()
		a.connectFresh()
		return
	}

	if err := a.mgr.Connect(cached.GatewayAddress, token); err != nil {
		dialog.ShowError(err, a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
	}
}

// connectFresh runs gpauth to get fresh SAML credentials, then calls the
// portal and gateway HTTP endpoints before starting the openconnect tunnel.
func (a *App) connectFresh() {
	if a.cfg.Portal == "" {
		return
	}
	ctx := a.authCtx
	if ctx == nil {
		ctx = context.Background()
	}

	authData, err := auth.RunGpauth(ctx, a.cfg.Portal, a.cfg.Browser)
	if err != nil {
		if ctx.Err() != nil {
			a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
			return
		}
		dialog.ShowError(fmt.Errorf("Authentication failed:\n%w", err), a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
		return
	}

	_ = auth.SaveCredentials(authData)

	portalCfg, err := portal.GetConfig(
		a.cfg.Portal,
		authData.Username,
		authData.PreloginCookie,
		"",
	)
	if err != nil {
		dialog.ShowError(fmt.Errorf("Portal config failed:\n%w", err), a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
		return
	}

	if len(portalCfg.Gateways) == 0 {
		dialog.ShowError(fmt.Errorf("No gateways returned by portal"), a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
		return
	}
	gw := portalCfg.Gateways[0]

	_ = auth.UpdatePortalCookies(portalCfg.PortalUserauthcookie, portalCfg.PrelogonUserauthcookie,
		gw.Address, gw.Name)

	token, err := portal.GatewayLogin(
		gw.Address,
		authData.Username,
		portalCfg.PortalUserauthcookie,
		portalCfg.PrelogonUserauthcookie,
	)
	if err != nil {
		dialog.ShowError(fmt.Errorf("Gateway login failed:\n%w", err), a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
		return
	}

	if err := a.mgr.Connect(gw.Address, token); err != nil {
		dialog.ShowError(err, a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
	}
}

// ---- settings dialog --------------------------------------------------------

func (a *App) showSettings() {
	portalEntry := widget.NewEntry()
	portalEntry.SetPlaceHolder("vpn.mycompany.io")
	portalEntry.SetText(a.cfg.Portal)

	browserSelect := widget.NewSelect(
		[]string{"embedded", "default", "firefox", "chrome", "chromium", "remote"},
		nil,
	)
	if a.cfg.Browser != "" {
		browserSelect.SetSelected(a.cfg.Browser)
	} else {
		browserSelect.SetSelected("default")
	}

	items := []*widget.FormItem{
		{Text: "Portal", Widget: portalEntry, HintText: "GlobalProtect portal hostname"},
		{Text: "Browser", Widget: browserSelect, HintText: "Browser used for SSO login"},
	}

	d := dialog.NewForm("Settings", "Save", "Cancel", items, func(saved bool) {
		if !saved {
			return
		}
		a.cfg.Portal = portalEntry.Text
		a.cfg.Browser = browserSelect.Selected
		_ = config.Save(a.cfg)
		auth.ClearCredentials() // portal changed → old credentials are invalid
		a.portalLabel.SetText(a.portalDisplay())
	}, a.window)
	d.Resize(fyne.NewSize(360, 200))
	d.Show()
}

// ---- colours / icons --------------------------------------------------------

var (
	colorGrey  = color.NRGBA{R: 150, G: 150, B: 150, A: 255}
	colorAmber = color.NRGBA{R: 255, G: 180, B: 0, A: 255}
	colorGreen = color.NRGBA{R: 40, G: 180, B: 70, A: 255}
)

// trayIcon returns a circle PNG resource for use in the system tray.
func trayIcon(c color.NRGBA) fyne.Resource {
	return fyne.NewStaticResource("tray.png", circleIcon(22, c))
}
