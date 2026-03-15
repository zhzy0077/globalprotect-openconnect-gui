// Package ui wires together the Fyne GUI, system tray, auth and VPN layers.
package ui

import (
	"context"
	"fmt"
	"image/color"
	"log"
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
	"fyne.io/systray"

	"github.com/nix-codes/gpoc-gui/internal/auth"
	"github.com/nix-codes/gpoc-gui/internal/config"
	"github.com/nix-codes/gpoc-gui/internal/credential"
	"github.com/nix-codes/gpoc-gui/internal/portal"
	"github.com/nix-codes/gpoc-gui/internal/vpn"
)

// App is the top-level application object.
type App struct {
	fyneApp fyne.App
	window  fyne.Window
	cfg     *config.Config
	mgr     *vpn.Manager

	statusDot    *canvas.Circle
	statusLabel  *widget.Label
	gatewayLabel *widget.Label
	connectBtn   *widget.Button
	portalLabel  *widget.Label

	trayMenu       *fyne.Menu
	trayConnect    *fyne.MenuItem
	trayDisconnect *fyne.MenuItem

	stateCh chan vpnStateMsg

	authCtx    context.Context
	authCancel context.CancelFunc
}

type vpnStateMsg struct {
	state   vpn.State
	gateway string
}

func NewApp() *App {
	a := &App{
		fyneApp: app.NewWithID("io.github.nix-codes.gpoc-gui"),
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

func (a *App) Shutdown() {
	a.mgr.Disconnect()
	a.fyneApp.Quit()
}

func (a *App) Run() {
	go func() {
		for msg := range a.stateCh {
			a.applyState(msg.state, msg.gateway)
		}
	}()

	a.fyneApp.Run()
	a.mgr.Disconnect()
}

func (a *App) buildWindow() {
	a.window = a.fyneApp.NewWindow("GlobalProtect-OpenConnect GUI")
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

	menu := fyne.NewMenu("GlobalProtect-OpenConnect GUI",
		a.trayConnect,
		a.trayDisconnect,
		fyne.NewMenuItemSeparator(),
		showItem,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", a.Shutdown),
	)

	a.trayMenu = menu
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(vpnDisconnectedIcon())
	systray.SetTooltip("GlobalProtect VPN")
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
		desk.SetSystemTrayIcon(vpnConnectedIcon())
	case vpn.StateConnecting, vpn.StateDisconnecting:
		desk.SetSystemTrayIcon(vpnConnectingIcon())
	default:
		desk.SetSystemTrayIcon(vpnDisconnectedIcon())
	}
}

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

func (a *App) onConnectPressed() {
	switch a.mgr.State() {
	case vpn.StateConnected, vpn.StateConnecting:
		a.mgr.Disconnect()
	default:
		a.applyState(vpn.StateConnecting, "")
		go a.doConnect()
	}
}

func checkSudoRule() error {
	cmd := exec.Command("sudo", "-n", "-l", "/usr/sbin/openconnect")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"Missing sudoers rule for openconnect.\n\n" +
				"Run the following to fix it:\n\n" +
				"  sudo make install\n\n" +
				"Or manually:\n" +
				"  echo '%%sudo ALL=(ALL) NOPASSWD: /usr/sbin/openconnect, /usr/bin/kill' \\\n" +
				"    | sudo tee /etc/sudoers.d/gpoc-gui\n" +
				"  sudo chmod 0440 /etc/sudoers.d/gpoc-gui",
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

	// Attempt seamless reconnect using cached credentials.
	if cached, err := auth.LoadCredentials(); err == nil &&
		cached.PortalCookieFromConfig != "" && cached.GatewayAddress != "" {
		go a.tryReconnect(cached)
		return
	}

	// No usable cache → browser-based first authentication.
	go a.connectFresh()
}

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

	// If only one gateway, use it directly
	if len(portalCfg.Gateways) == 1 {
		gw := portalCfg.Gateways[0]
		a.cfg.Gateway = gw.Address
		_ = config.Save(a.cfg)
		a.gatewayLabel.SetText(fmt.Sprintf("Gateway: %s", gw.Name))
		a.gatewayLabel.Show()
		a.connectToGateway(gw, portalCfg, authData)
		return
	}

	// Multiple gateways - show selection dialog
	// If a specific gateway is configured and exists in the list, use it directly
	if a.cfg.Gateway != "" {
		for _, g := range portalCfg.Gateways {
			if g.Address == a.cfg.Gateway {
				log.Printf("Using configured gateway: %s (address: %s)", g.Name, g.Address)
				a.gatewayLabel.SetText(fmt.Sprintf("Gateway: %s", g.Name))
				a.gatewayLabel.Show()
				a.connectToGateway(g, portalCfg, authData)
				return
			}
		}
		log.Printf("Configured gateway %s not found in portal response, showing selection dialog", a.cfg.Gateway)
	}
	var gwNames []string
	for _, g := range portalCfg.Gateways {
		name := g.Name
		if g.Address != g.Name {
			name = fmt.Sprintf("%s (%s)", g.Name, g.Address)
		}
		gwNames = append(gwNames, name)
	}

	gwSelect := widget.NewSelect(gwNames, nil)
	gwSelect.SetSelected(gwNames[0])

	items := []*widget.FormItem{
		{Text: "Gateway", Widget: gwSelect},
	}

	d := dialog.NewForm("Select Gateway", "Connect", "Cancel", items, func(ok bool) {
		if !ok {
			a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
			return
		}
		gw := portalCfg.Gateways[gwSelect.SelectedIndex()]
		log.Printf("Selected gateway: %s (address: %s)", gw.Name, gw.Address)
		a.cfg.Gateway = gw.Address
		if err := config.Save(a.cfg); err != nil {
			log.Printf("Failed to save gateway to config: %v", err)
		} else {
			log.Printf("Saved gateway to config: %s", a.cfg.Gateway)
		}
		a.gatewayLabel.SetText(fmt.Sprintf("Gateway: %s", gw.Name))
		a.gatewayLabel.Show()
		a.connectToGateway(gw, portalCfg, authData)
	}, a.window)
	d.Resize(fyne.NewSize(360, 160))
	d.Show()
}

func (a *App) connectToGateway(gw portal.Gateway, portalCfg *portal.Config, authData *auth.SamlAuthData) {
	token, err := portal.GatewayLogin(
		gw.Address,
		authData.Username,
		portalCfg.PortalUserauthcookie,
		portalCfg.PrelogonUserauthcookie,
	)
	if err != nil {
		// Retry with fresh auth on any error (matches gpclient behavior)
		log.Printf("Gateway login with portal cookie failed, retrying with fresh auth: %v", err)
		token, err = a.freshGatewayAuth(gw.Address)
	}

	if err != nil {
		dialog.ShowError(fmt.Errorf("Gateway login failed:\n%w", err), a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
		return
	}

	_ = auth.UpdatePortalCookies(portalCfg.PortalUserauthcookie, portalCfg.PrelogonUserauthcookie,
		gw.Address, gw.Name)

	if err := a.mgr.Connect(gw.Address, token); err != nil {
		dialog.ShowError(err, a.window)
		a.stateCh <- vpnStateMsg{state: vpn.StateDisconnected}
	}
}

// freshGatewayAuth performs fresh authentication to a gateway (without portal cookie).
func (a *App) freshGatewayAuth(gateway string) (string, error) {
	ctx := a.authCtx
	if ctx == nil {
		ctx = context.Background()
	}

	prelogin, err := portal.PerformPrelogin(gateway, true)
	if err != nil {
		return "", fmt.Errorf("gateway prelogin failed: %w", err)
	}

	var cred credential.Credential
	switch p := prelogin.(type) {
	case *portal.SamlPrelogin:
		authData, err := auth.RunGpauthGateway(ctx, gateway, a.cfg.Browser)
		if err != nil {
			return "", fmt.Errorf("gateway auth failed: %w", err)
		}
		cred = &credential.PreloginCredential{
			User:           authData.Username,
			PreloginCookie: authData.PreloginCookie,
		}
	case *portal.StandardPrelogin:
		return "", fmt.Errorf("password auth not supported, gateway requires: %s", p.AuthMessage)
	default:
		return "", fmt.Errorf("unsupported auth type for gateway: %T", prelogin)
	}

	token, err := portal.GatewayLoginWithCredential(gateway, cred)
	if err != nil {
		return "", fmt.Errorf("gateway login failed: %w", err)
	}

	return token, nil
}

func (a *App) showSettings() {
	portalEntry := widget.NewEntry()
	portalEntry.SetPlaceHolder("vpn.mycompany.io")
	portalEntry.SetText(a.cfg.Portal)

	gatewayEntry := widget.NewEntry()
	gatewayEntry.SetPlaceHolder("gateway.company.com (optional)")
	log.Printf("Loading gateway into settings panel: '%s'", a.cfg.Gateway)
	gatewayEntry.SetText(a.cfg.Gateway)

	browserSelect := widget.NewSelect(
		[]string{"embedded", "default", "firefox", "chrome", "chromium", "microsoft-edge-stable", "remote"},
		nil,
	)
	if a.cfg.Browser != "" {
		browserSelect.SetSelected(a.cfg.Browser)
	} else {
		browserSelect.SetSelected("embedded")
	}

	items := []*widget.FormItem{
		{Text: "Portal", Widget: portalEntry, HintText: "GlobalProtect portal or gateway hostname"},
		{Text: "Gateway", Widget: gatewayEntry, HintText: "Optional: specific gateway to connect"},
		{Text: "Browser", Widget: browserSelect, HintText: "Browser for SSO login"},
	}

	d := dialog.NewForm("Settings", "Save", "Cancel", items, func(saved bool) {
		if !saved {
			return
		}
		a.cfg.Portal = portalEntry.Text
		a.cfg.Gateway = gatewayEntry.Text
		a.cfg.Browser = browserSelect.Selected
		_ = config.Save(a.cfg)
		auth.ClearCredentials()
		a.portalLabel.SetText(a.portalDisplay())
	}, a.window)
	d.Resize(fyne.NewSize(400, 280))
	d.Show()
}

var (
	colorGrey  = color.NRGBA{R: 150, G: 150, B: 150, A: 255}
	colorAmber = color.NRGBA{R: 255, G: 180, B: 0, A: 255}
	colorGreen = color.NRGBA{R: 40, G: 180, B: 70, A: 255}
)
