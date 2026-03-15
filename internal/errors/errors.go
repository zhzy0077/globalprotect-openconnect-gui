// Package errors implements GlobalProtect-specific error types.
// Mirrors gpclient's error handling for portal/gateway operations.
package errors

import "fmt"

// PortalError represents a portal/gateway-related error that may be recoverable.
// Used to determine if we should fallback to gateway authentication.
// Mirrors gpclient::PortalError
type PortalError struct {
	Op  string // Operation name: "prelogin", "getconfig", "gateway_login"
	Err error  // Underlying error
}

func (e *PortalError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *PortalError) Unwrap() error {
	return e.Err
}

// NewPortalError creates a new PortalError.
// Use this for errors that should trigger gateway fallback.
func NewPortalError(op string, err error) *PortalError {
	return &PortalError{Op: op, Err: err}
}

// IsPortalError checks if an error is a PortalError.
// Mirrors gpclient: err.root_cause().downcast_ref::<PortalError>().is_some()
func IsPortalError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*PortalError)
	return ok
}
