// Package vpn manages the gpclient subprocess that owns the VPN tunnel.
package vpn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// State represents the current VPN connection state.
type State int

const (
	StateDisconnected  State = iota
	StateConnecting          // gpclient launched, prelogin / portal-config in progress
	StateConnected           // tunnel is up
	StateDisconnecting       // SIGINT sent, waiting for process exit
	StateAuthFailed          // server returned auth-failed
	StateError               // unexpected error
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting…"
	case StateConnected:
		return "Connected"
	case StateDisconnecting:
		return "Disconnecting…"
	case StateAuthFailed:
		return "Auth failed"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Manager owns the lifecycle of the gpclient subprocess and notifies the UI
// of state changes via the OnStateChange callback.
type Manager struct {
	mu            sync.Mutex
	state         State
	gateway       string // last known gateway from log output
	cmd           *exec.Cmd
	cancelMonitor context.CancelFunc
	OnStateChange func(State, string) // state, gateway name
}

func New(onChange func(State, string)) *Manager {
	return &Manager{
		state:         StateDisconnected,
		OnStateChange: onChange,
	}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) Gateway() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gateway
}

func (m *Manager) setState(s State, gw string) {
	m.mu.Lock()
	m.state = s
	if gw != "" {
		m.gateway = gw
	}
	m.mu.Unlock()
	if m.OnStateChange != nil {
		m.OnStateChange(s, gw)
	}
}

// Connect launches `sudo gpclient connect <portal> --cookie-on-stdin`, pipes
// credJSON as a single line to stdin, then monitors the process output to
// drive state transitions.
func (m *Manager) Connect(portal, credJSON string) error {
	m.mu.Lock()
	if m.state != StateDisconnected && m.state != StateAuthFailed && m.state != StateError {
		m.mu.Unlock()
		return fmt.Errorf("cannot connect: current state is %s", m.state)
	}
	m.mu.Unlock()

	m.setState(StateConnecting, "")

	cmd := exec.Command("sudo", "-n", "gpclient", "connect", portal, "--cookie-on-stdin")

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		m.setState(StateDisconnected, "")
		return fmt.Errorf("stdin pipe: %w", err)
	}

	// Merge stdout + stderr so we catch all log lines.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		m.setState(StateDisconnected, "")
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		m.setState(StateDisconnected, "")
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		m.setState(StateDisconnected, "")
		return fmt.Errorf("start gpclient: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	// Write the credential JSON to stdin.  gpclient does a prelogin HTTP call
	// first so we wait a moment to avoid a race with its stdin read.
	go func() {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprintln(stdinPipe, credJSON)
		stdinPipe.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancelMonitor = cancel
	m.mu.Unlock()

	// Monitor both pipes concurrently; feed all lines into a single channel.
	// Close lines when both readers finish so monitor() can detect EOF.
	lines := make(chan string, 128)
	var wg sync.WaitGroup
	for _, r := range []io.Reader{stdoutPipe, stderrPipe} {
		wg.Add(1)
		go func(rd io.Reader) {
			defer wg.Done()
			sc := bufio.NewScanner(rd)
			for sc.Scan() {
				select {
				case lines <- sc.Text():
				case <-ctx.Done():
					return
				}
			}
		}(r)
	}
	go func() {
		wg.Wait()
		close(lines)
	}()

	go m.monitor(ctx, cancel, cmd, lines)

	return nil
}

// monitor runs in a goroutine, parses gpclient log output to drive state
// transitions, then waits for the process to exit before emitting the final state.
func (m *Manager) monitor(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, lines <-chan string) {
	defer cancel()

	authFailed := false
	var gateway string

loop:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				break loop
			}
			m.parseLine(line, &authFailed, &gateway)
		case <-ctx.Done():
			break loop
		}
	}

	_ = cmd.Wait()

	m.mu.Lock()
	m.cmd = nil
	m.mu.Unlock()

	if authFailed {
		m.setState(StateAuthFailed, "")
	} else {
		m.setState(StateDisconnected, "")
	}
}

// parseLine inspects a single log line and updates flags / state accordingly.
func (m *Manager) parseLine(line string, authFailed *bool, gateway *string) {
	switch {
	case strings.Contains(line, "auth-failed"):
		*authFailed = true

	// gpclient writes the PID file exactly when openconnect has the tunnel up.
	case strings.Contains(line, "Wrote PID") && strings.Contains(line, "gpclient.lock"):
		m.setState(StateConnected, *gateway)

	// Capture gateway name from log lines like:
	//   "Connecting to the selected gateway: EU-West (gw.company.com)"
	case strings.Contains(line, "Connecting to the") && strings.Contains(line, "gateway"):
		if parts := strings.SplitN(line, "gateway: ", 2); len(parts) == 2 {
			*gateway = strings.TrimSpace(parts[1])
		}
		m.setState(StateConnecting, *gateway)

	case strings.Contains(line, "Received the interrupt signal"):
		m.setState(StateDisconnecting, "")
	}
}

// Disconnect runs "sudo gpclient disconnect" to cleanly shut down the tunnel.
// gpclient and sudo both run as root; our process cannot signal them directly.
func (m *Manager) Disconnect() {
	m.mu.Lock()
	state := m.state
	m.mu.Unlock()

	if state == StateDisconnected || state == StateDisconnecting {
		return
	}

	m.setState(StateDisconnecting, "")

	go func() {
		_ = exec.Command("sudo", "-n", "gpclient", "disconnect").Run()
	}()
}
