package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gpclient-gui/gpclient-gui/internal/ui"
)

func main() {
	a := ui.NewApp()

	// Disconnect VPN on SIGTERM (e.g. systemd stop, kill without -9).
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		a.Shutdown()
		os.Exit(0)
	}()

	a.Run()
}
