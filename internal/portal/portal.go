// Package portal implements the GlobalProtect portal HTTP calls natively,
// allowing seamless reconnect without re-opening the SAML browser flow.
package portal

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/nix-codes/gpoc-gui/internal/credential"
	"github.com/nix-codes/gpoc-gui/internal/errors"
)

const userAgent = "PAN GlobalProtect/6.3.0-33 (Linux)"

// Gateway is a VPN gateway advertised by the portal.
type Gateway struct {
	Name    string
	Address string
}

// Config holds the portal's response from getconfig.esp.
// Mirrors gpclient::PortalConfig
type Config struct {
	Portal                 string
	PortalUserauthcookie   string
	PrelogonUserauthcookie string
	Gateways               []Gateway
	Version                string
	username               string // internal field for AuthCookie generation
}

// AuthCookie returns an AuthCookieCredential from the config.
// Mirrors gpclient: portal_config.auth_cookie()
func (c *Config) AuthCookie() *credential.AuthCookieCredential {
	return credential.FromAuthCookie(
		c.username,
		c.PortalUserauthcookie,
		c.PrelogonUserauthcookie,
	)
}

// Prelogin is the interface for prelogin results.
// Mirrors gpclient's Prelogin enum
type Prelogin interface {
	IsSAML() bool
	IsGateway() bool
	Region() string
}

// SamlPrelogin represents SAML authentication prelogin result.
// Mirrors gpclient::Prelogin::Saml
type SamlPrelogin struct {
	PreloginRegion        string
	IsGatewayFlag         bool
	PreloginSAMLRequest   string
	SupportDefaultBrowser bool
}

func (s *SamlPrelogin) IsSAML() bool        { return true }
func (s *SamlPrelogin) IsGateway() bool     { return s.IsGatewayFlag }
func (s *SamlPrelogin) Region() string      { return s.PreloginRegion }
func (s *SamlPrelogin) SAMLRequest() string { return s.PreloginSAMLRequest }


// StandardPrelogin represents standard password authentication prelogin result.
// Mirrors gpclient::Prelogin::Standard
type StandardPrelogin struct {
	PreloginRegion string
	IsGatewayFlag  bool
	AuthMessage    string
	LabelUsername  string
	LabelPassword  string
}

func (s *StandardPrelogin) IsSAML() bool    { return false }

func (s *StandardPrelogin) IsGateway() bool { return s.IsGatewayFlag }
func (s *StandardPrelogin) Region() string  { return s.PreloginRegion }


// Prelogin calls the prelogin.esp endpoint and returns the prelogin type.
// Mirrors gpclient::prelogin
func PerformPrelogin(server string, isGateway bool) (Prelogin, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "linux"
	}

	path := "global-protect"
	if isGateway {
		path = "ssl-vpn"
	}

	endpoint := fmt.Sprintf("https://%s/%s/prelogin.esp", server, path)

	form := url.Values{}
	form.Set("clientos", "Linux")
	form.Set("os-version", "Linux")
	form.Set("clientVer", "4100")
	form.Set("computer", hostname)
	form.Set("host-id", "")
	form.Set("tmp", "tmp")
	form.Set("default-browser", "1")
	form.Set("cas-support", "yes")
	form.Set("ipv6-support", "yes")

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.NewPortalError("prelogin", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.NewPortalError("prelogin", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.NewPortalError("prelogin", fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.NewPortalError("prelogin", err)
	}

	return parsePreloginResponse(body, isGateway)
}

// parsePreloginResponse parses the prelogin XML response.
func parsePreloginResponse(body []byte, isGateway bool) (Prelogin, error) {
	bodyStr := string(body)

	// Extract status
	status := extractXMLValue(bodyStr, "status")
	if status != "" && !strings.EqualFold(status, "SUCCESS") {
		msg := extractXMLValue(bodyStr, "msg")
		if msg == "" {
			msg = status
		}
		return nil, errors.NewPortalError("prelogin", fmt.Errorf("status: %s", msg))
	}


	region := extractXMLValue(bodyStr, "region")
	if region == "" {
		region = "Unknown"
	}

	// Check for SAML
	samlMethod := extractXMLValue(bodyStr, "saml-auth-method")
	samlRequest := extractXMLValue(bodyStr, "saml-request")

	if samlMethod != "" && samlRequest != "" {
		// Decode base64 SAML request
		decodedSAML, err := base64.StdEncoding.DecodeString(samlRequest)
		if err != nil {
			// If decoding fails, use as-is
			decodedSAML = []byte(samlRequest)
		}

		supportDefaultBrowser := extractXMLValue(bodyStr, "saml-default-browser") == "yes"

		return &SamlPrelogin{
			PreloginRegion:        region,
			IsGatewayFlag:         isGateway,
			PreloginSAMLRequest:   string(decodedSAML),
			SupportDefaultBrowser: supportDefaultBrowser,
		}, nil

	}

	// Standard prelogin
	labelUsername := extractXMLValue(bodyStr, "username-label")
	if labelUsername == "" {
		labelUsername = "Username"
	}
	labelPassword := extractXMLValue(bodyStr, "password-label")
	if labelPassword == "" {
		labelPassword = "Password"
	}
	authMessage := extractXMLValue(bodyStr, "authentication-message")
	if authMessage == "" {
		authMessage = "Please enter the login credentials"
	}

	return &StandardPrelogin{
		PreloginRegion: region,
		IsGatewayFlag:  isGateway,
		AuthMessage:    authMessage,
		LabelUsername:  labelUsername,
		LabelPassword:  labelPassword,
	}, nil


}

// extractXMLValue extracts a value from XML by tag name.
func extractXMLValue(xml, tag string) string {
	start := strings.Index(xml, "<"+tag+">")
	if start == -1 {
		start = strings.Index(xml, "<"+strings.ToUpper(tag)+">")
	}
	if start == -1 {
		return ""
	}
	start += len(tag) + 2

	endTag := "</" + tag + ">"
	end := strings.Index(xml[start:], endTag)
	if end == -1 {
		endTag = "</" + strings.ToUpper(tag) + ">"
		end = strings.Index(xml[start:], endTag)
	}
	if end == -1 {
		return ""
	}

	return xml[start : start+end]
}

// GetConfig calls POST https://<portal>/global-protect/getconfig.esp.
//
// For fresh auth pass preloginCookie (from SAML) and leave portalUserauthcookie empty.
// For reconnect pass portalUserauthcookie and leave preloginCookie empty.
//
// Returns an error if the server responds with a non-200 status or if the
// portal-userauthcookie field is absent/empty (caller should fall back to
// fresh auth in that case).
func GetConfig(portal, username, preloginCookie, portalUserauthcookie string) (*Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "linux"
	}

	form := url.Values{}
	form.Set("user", username)
	form.Set("passwd", "")
	form.Set("prelogin-cookie", preloginCookie)
	form.Set("portal-userauthcookie", portalUserauthcookie)
	form.Set("portal-prelogonuserauthcookie", "")
	form.Set("prot", "https:")
	form.Set("jnlpReady", "jnlpReady")
	form.Set("ok", "Login")
	form.Set("direct", "yes")
	form.Set("ipv6-support", "yes")
	form.Set("clientVer", "4100")
	form.Set("clientos", "Linux")
	form.Set("computer", hostname)
	form.Set("server", portal)
	form.Set("host", portal)

	endpoint := "https://" + portal + "/global-protect/getconfig.esp"
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.NewPortalError("getconfig", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.NewPortalError("getconfig", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.NewPortalError("getconfig", fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.NewPortalError("getconfig", err)
	}

	cfg, err := parseGetConfigResponse(body)
	if err != nil {
		return nil, err
	}

	if cfg.PortalUserauthcookie == "" {
		return nil, errors.NewPortalError("getconfig", fmt.Errorf("empty portal-userauthcookie (auth rejected)"))
	}

	// Save username for AuthCookie generation
	cfg.username = username

	return cfg, nil
}

// GatewayLogin calls POST https://<gateway>/ssl-vpn/login.esp and returns
// the URL-encoded openconnect cookie token.
func GatewayLogin(gateway, username, portalUserauthcookie, prelogonUserauthcookie string) (string, error) {
	cred := credential.FromAuthCookie(username, portalUserauthcookie, prelogonUserauthcookie)
	return GatewayLoginWithCredential(gateway, cred)
}

// GatewayLoginWithCredential calls POST https://<gateway>/ssl-vpn/login.esp
// using a Credential interface and returns the URL-encoded openconnect cookie token.
// Mirrors gpclient::gateway_login
func GatewayLoginWithCredential(gateway string, cred credential.Credential) (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "linux"
	}

	// Get params from credential
	params := cred.ToParams()

	// Add fixed parameters
	params.Set("prot", "https:")
	params.Set("jnlpReady", "jnlpReady")
	params.Set("ok", "Login")
	params.Set("direct", "yes")
	params.Set("ipv6-support", "yes")
	params.Set("clientVer", "4100")
	params.Set("clientos", "Linux")
	params.Set("computer", hostname)
	params.Set("server", gateway)

	endpoint := "https://" + gateway + "/ssl-vpn/login.esp"
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return "", errors.NewPortalError("gateway_login", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.NewPortalError("gateway_login", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.NewPortalError("gateway_login", fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.NewPortalError("gateway_login", err)
	}

	return parseGatewayLoginResponse(body, hostname)
}

// ---- XML parsers ------------------------------------------------------------

// getConfigXML mirrors the structure of the getconfig.esp XML response.
type getConfigXML struct {
	XMLName              xml.Name `xml:"policy"`
	PortalUserauthcookie string   `xml:"portal-userauthcookie"`
	PrelogonCookie       string   `xml:"portal-prelogonuserauthcookie"`
	Gateways             struct {
		External struct {
			List struct {
				Entries []gatewayEntry `xml:"entry"`
			} `xml:"list"`
		} `xml:"external"`
	} `xml:"gateways"`
}

type gatewayEntry struct {
	Name    string `xml:"name,attr"`
	Address string `xml:"address"`
}

func parseGetConfigResponse(body []byte) (*Config, error) {
	var doc getConfigXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, errors.NewPortalError("getconfig", fmt.Errorf("parse response: %w", err))
	}

	cfg := &Config{
		PortalUserauthcookie:   doc.PortalUserauthcookie,
		PrelogonUserauthcookie: doc.PrelogonCookie,
	}
	for _, e := range doc.Gateways.External.List.Entries {
		addr := e.Address
		if addr == "" {
			addr = e.Name // portal uses name attr as the hostname when no <address> child
		}
		if addr != "" {
			cfg.Gateways = append(cfg.Gateways, Gateway{
				Name:    e.Name,
				Address: addr,
			})
		}
	}
	return cfg, nil
}

// loginXML mirrors the structure of the login.esp XML response.
// The response contains a <jnlp> element with a list of <argument> elements.
type loginXML struct {
	XMLName   xml.Name `xml:"jnlp"`
	Arguments []string `xml:"application-desc>argument"`
}

func parseGatewayLoginResponse(body []byte, hostname string) (string, error) {
	var doc loginXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return "", errors.NewPortalError("gateway_login", fmt.Errorf("parse response: %w", err))
	}

	args := doc.Arguments
	get := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}

	authcookie := get(1)
	portal := get(3)
	user := get(4)
	domain := get(7)
	preferredIP := get(15)

	if authcookie == "" {
		return "", errors.NewPortalError("gateway_login", fmt.Errorf("empty authcookie in response"))
	}

	token := url.Values{}
	token.Set("authcookie", authcookie)
	token.Set("portal", portal)
	token.Set("user", user)
	token.Set("domain", domain)
	token.Set("preferred-ip", preferredIP)
	token.Set("computer", hostname)

	return token.Encode(), nil
}
