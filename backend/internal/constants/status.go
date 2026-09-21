package constants

// Shared status values are mirrored in frontend/src/types/status.ts. Keeping
// the lists explicit makes state-machine drift visible during code review.

type GateState string

const (
	GateStateOpen   GateState = "open"
	GateStateClosed GateState = "closed"
	GateStateMoving GateState = "moving"
	GateStateLocked GateState = "locked"
)

var AllGateState = []string{"open", "closed", "moving", "locked"}

type DirectiveState string

const (
	DirectiveStateDraft     DirectiveState = "draft"
	DirectiveStatePending   DirectiveState = "pending"
	DirectiveStateApproved  DirectiveState = "approved"
	DirectiveStateExecuting DirectiveState = "executing"
	DirectiveStateCompleted DirectiveState = "completed"
	DirectiveStateAborted   DirectiveState = "aborted"
)

var AllDirectiveState = []string{"draft", "pending", "approved", "executing", "completed", "aborted"}

var ReservoirTransitions = map[string]map[string]bool{
	"normal":     {"warning": true, "critical": true},
	"warning":    {"critical": true, "restricted": true, "normal": true},
	"critical":   {"restricted": true, "warning": true},
	"restricted": {"critical": true},
}

var GateUnitTransitions = map[string]map[string]bool{
	"open":   {"moving": true, "locked": true},
	"closed": {"moving": true, "locked": true},
	"moving": {"open": true, "closed": true, "locked": true},
	"locked": {"closed": true},
}

var OperationDirectiveTransitions = map[string]map[string]bool{
	"draft":     {"pending": true},
	"pending":   {"approved": true, "aborted": true},
	"approved":  {"executing": true, "aborted": true},
	"executing": {"completed": true, "aborted": true},
	"completed": {},
	"aborted":   {},
}

type PermitState string

const (
	PermitStateRequested   PermitState = "requested"
	PermitStateApproved    PermitState = "approved"
	PermitStateConsumed    PermitState = "consumed"
	PermitStateRejected    PermitState = "rejected"
	PermitStateInvalidated PermitState = "invalidated"
	PermitStateExpired     PermitState = "expired"
)

var AllPermitState = []string{"requested", "approved", "consumed", "rejected", "invalidated", "expired"}

var ExecutionConfirmationTransitions = map[string]map[string]bool{
	"pending":   {"confirmed": true, "failed": true},
	"confirmed": {},
	"failed":    {"cancelled": true},
	"cancelled": {},
}

// DispatchPermitTransitions guards 调度许可 lifecycle movement. Only one
// requested/approved permit per directive is allowed, so every terminal side
// path (reject, drift invalidation, expiry) must be explicit.
var DispatchPermitTransitions = map[string]map[string]bool{
	"requested":   {"approved": true, "rejected": true, "invalidated": true, "expired": true},
	"approved":    {"consumed": true, "invalidated": true, "expired": true},
	"consumed":    {},
	"rejected":    {},
	"invalidated": {},
	"expired":     {},
}

func CanTransition(graph map[string]map[string]bool, from, to string) bool {
	targets, exists := graph[from]
	return exists && targets[to]
}
