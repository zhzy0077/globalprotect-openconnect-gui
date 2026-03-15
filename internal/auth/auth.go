// Package auth handles SAML authentication via the gpauth binary and
// caches the resulting portal-userauthcookie for reconnect attempts.
package auth

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SamlAuthData mirrors the success payload that gpauth prints to stdout.
type SamlAuthData struct {
	Username             string `json:"username"`
	PreloginCookie       string `json:"preloginCookie"`
	PortalUserauthcookie string `json:"portalUserauthcookie"`
	Token                string `json:"token,omitempty"`
}

// samlAuthResult is the top-level JSON envelope gpauth emits.
type samlAuthResult struct {
	Success *SamlAuthData `json:"success,omitempty"`
	Failure *string       `json:"failure,omitempty"`
}

// CachedAuth is persisted to disk between sessions.
type CachedAuth struct {
	SamlAuthData
	PortalCookieFromConfig   string    `json:"portalCookieFromConfig,omitempty"`
	PrelogonCookieFromConfig string    `json:"prelogonCookieFromConfig,omitempty"`
	GatewayAddress           string    `json:"gatewayAddress,omitempty"`
	GatewayName              string    `json:"gatewayName,omitempty"`
	SavedAt                  time.Time `json:"savedAt"`
}

// RunGpauth launches `gpauth <portal> [--browser [browser]]`, waits for the
// user to complete the browser-based login, and returns the parsed auth data.
func RunGpauth(ctx context.Context, portal, browser string) (*SamlAuthData, error) {
	args := []string{portal}
	switch browser {
	case "embedded", "":
		// no --browser flag → gpauth opens its built-in WebKitGTK window
	case "default":
		args = append(args, "--browser")
	default:
		args = append(args, "--browser", browser)
	}

	cmd := exec.CommandContext(ctx, "gpauth", args...)
	cmd.Stderr = os.Stderr // let gpauth log to our stderr for debugging

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gpauth: %w", err)
	}

	var result samlAuthResult
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if err := json.Unmarshal([]byte(scanner.Text()), &result); err == nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read gpauth output: %w", err)
	}

	// Wait even if we already read the line; ignore exit code because gpauth
	// sometimes exits non-zero after writing valid JSON.
	_ = cmd.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if result.Success == nil {
		if result.Failure != nil {
			return nil, fmt.Errorf("gpauth: %s", *result.Failure)
		}
		return nil, fmt.Errorf("gpauth produced no auth data")
	}

	return result.Success, nil
}

// RunGpauthGateway launches `gpauth <gateway> --gateway [--browser [browser]]` for gateway authentication.
func RunGpauthGateway(ctx context.Context, gateway, browser string) (*SamlAuthData, error) {
	args := []string{gateway, "--gateway"}
	switch browser {
	case "embedded", "":
		// no --browser flag
	case "default":
		args = append(args, "--browser")
	default:
		args = append(args, "--browser", browser)
	}

	cmd := exec.CommandContext(ctx, "gpauth", args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gpauth: %w", err)
	}

	var result samlAuthResult
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if err := json.Unmarshal([]byte(scanner.Text()), &result); err == nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read gpauth output: %w", err)
	}

	_ = cmd.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if result.Success == nil {
		if result.Failure != nil {
			return nil, fmt.Errorf("gpauth: %s", *result.Failure)
		}
		return nil, fmt.Errorf("gpauth produced no auth data")
	}

	return result.Success, nil
}

// ToGpclientJSON returns the JSON line that should be written to openconnect's
// stdin when using --cookie-on-stdin.
func (d *SamlAuthData) ToGpclientJSON() (string, error) {
	cookie := d.PreloginCookie
	if cookie == "" {
		cookie = d.PortalUserauthcookie
	}
	env := map[string]interface{}{
		"success": map[string]interface{}{
			"username":             d.Username,
			"preloginCookie":       cookie,
			"portalUserauthcookie": d.PortalUserauthcookie,
			"token":                nil,
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ToReconnectJSON builds a credential JSON that tries to reuse the long-lived
// portalUserauthcookie as the preloginCookie.  Some servers accept this, which
// avoids reopening the browser.  If the server returns auth-failed the caller
// should discard the cache and perform a fresh gpauth run.
func (d *SamlAuthData) ToReconnectJSON() (string, error) {
	cookie := d.PortalUserauthcookie
	if cookie == "" {
		cookie = d.PreloginCookie
	}
	env := map[string]interface{}{
		"success": map[string]interface{}{
			"username":             d.Username,
			"preloginCookie":       cookie,
			"portalUserauthcookie": nil,
			"token":                nil,
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- credential cache -------------------------------------------------------

func cacheFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gpoc-gui", "auth.json"), nil
}

// SaveCredentials writes auth data to disk (mode 0600).
func SaveCredentials(data *SamlAuthData) error {
	path, err := cacheFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cached := &CachedAuth{SamlAuthData: *data, SavedAt: time.Now()}
	b, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// LoadCredentials returns the full cached auth record, or an error if none
// exist or the file cannot be parsed.
func LoadCredentials() (*CachedAuth, error) {
	path, err := cacheFile()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cached CachedAuth
	if err := json.Unmarshal(b, &cached); err != nil {
		return nil, err
	}
	return &cached, nil
}

// UpdatePortalCookies overwrites the portal cookie and gateway fields in the
// on-disk cache, leaving the SAML data intact.
func UpdatePortalCookies(portalCookie, prelogonCookie, gatewayAddress, gatewayName string) error {
	cached, err := LoadCredentials()
	if err != nil {
		return err
	}
	cached.PortalCookieFromConfig = portalCookie
	cached.PrelogonCookieFromConfig = prelogonCookie
	cached.GatewayAddress = gatewayAddress
	cached.GatewayName = gatewayName
	path, err := cacheFile()
	if err != nil {
		return err
	}
	b, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ClearCredentials removes the on-disk credential cache.
func ClearCredentials() {
	if path, err := cacheFile(); err == nil {
		_ = os.Remove(path)
	}
}
