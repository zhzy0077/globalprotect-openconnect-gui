// Package credential implements the GlobalProtect credential types.
// This package mirrors the Credential enum from gpclient.
package credential

import "net/url"

// Credential is the interface for all credential types.
// Mirrors gpclient's Credential enum.
type Credential interface {
	Username() string
	ToParams() url.Values
}

// PreloginCredential represents credentials obtained after SAML authentication.
// Mirrors gpclient::Credential::Prelogin
type PreloginCredential struct {
	User           string
	PreloginCookie string
	Token          string
}

func (c *PreloginCredential) Username() string { return c.User }

func (c *PreloginCredential) ToParams() url.Values {
	v := url.Values{}
	v.Set("user", c.User)
	v.Set("prelogin-cookie", c.PreloginCookie)
	if c.Token != "" {
		v.Set("token", c.Token)
	}
	return v
}

// AuthCookieCredential represents credentials obtained from portal config.
// Contains portal-userauthcookie for gateway login.
// Mirrors gpclient::Credential::AuthCookie
type AuthCookieCredential struct {
	User                   string
	PortalUserauthcookie   string
	PrelogonUserauthcookie string
}

func (c *AuthCookieCredential) Username() string { return c.User }

func (c *AuthCookieCredential) ToParams() url.Values {
	v := url.Values{}
	v.Set("user", c.User)
	v.Set("portal-userauthcookie", c.PortalUserauthcookie)
	v.Set("portal-prelogonuserauthcookie", c.PrelogonUserauthcookie)
	return v
}

// PasswordCredential represents standard username/password credentials.
// Mirrors gpclient::Credential::Password
type PasswordCredential struct {
	User     string
	Password string
}

func (c *PasswordCredential) Username() string { return c.User }

func (c *PasswordCredential) ToParams() url.Values {
	v := url.Values{}
	v.Set("user", c.User)
	v.Set("passwd", c.Password)
	return v
}

// FromAuthCookie creates an AuthCookieCredential from portal config values.
// Mirrors gpclient: portal_config.auth_cookie().into()
func FromAuthCookie(username, portalCookie, prelogonCookie string) *AuthCookieCredential {
	return &AuthCookieCredential{
		User:                   username,
		PortalUserauthcookie:   portalCookie,
		PrelogonUserauthcookie: prelogonCookie,
	}
}
