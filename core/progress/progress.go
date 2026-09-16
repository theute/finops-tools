// Package progress defines the shared long-running-step reporter interface
// used across core packages (cost, report, account, ...).
package progress

// Reporter reports human-readable progress steps for long-running operations.
type Reporter interface {
	Step(message string)
}
