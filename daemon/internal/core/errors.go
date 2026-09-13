package core

import "fmt"

// INFO: The daemon distinguishes what the user did wrong from what the daemon got
// wrong. Only the latter is worth a stack in the log; the former is an ordinary
// answer the GUI turns into a sentence.

// NotFoundError names something that does not exist.
type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What }

func notFound(format string, args ...any) error {
	return &NotFoundError{What: fmt.Sprintf(format, args...)}
}

// InvalidError reports input the daemon will not act on.
type InvalidError struct{ Why string }

func (e *InvalidError) Error() string { return e.Why }

func invalid(format string, args ...any) error {
	return &InvalidError{Why: fmt.Sprintf(format, args...)}
}

// ConflictError reports a request that clashes with the current state.
type ConflictError struct{ Why string }

func (e *ConflictError) Error() string { return e.Why }

func conflict(format string, args ...any) error {
	return &ConflictError{Why: fmt.Sprintf(format, args...)}
}
