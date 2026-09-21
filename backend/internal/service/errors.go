package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrForbidden         = errors.New("role is not permitted for this operation")
	ErrTwoPersonRequired = errors.New("submitter and approver must be different users")
	ErrImmutableState    = errors.New("record can no longer be edited in its current state")
	ErrPermitRequired    = errors.New("gate action requires a valid dispatch permit")
	ErrPermitConflict    = errors.New("only one dispatch permit can be active for a directive")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
)
