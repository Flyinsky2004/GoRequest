package core

import (
	"errors"
	"fmt"
)

// ConfigError reports a problem in project definition, parameters, missing
// resources or unsupported formats, discovered before a request is sent. The
// CLI maps it to exit code 2.
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

func configErrorf(format string, args ...any) error {
	return &ConfigError{Err: fmt.Errorf(format, args...)}
}

// IsConfig reports whether err is a ConfigError.
func IsConfig(err error) bool {
	var ce *ConfigError
	return errors.As(err, &ce)
}

// RunError reports a failure while performing a request: network, TLS,
// connection or protocol failure. The CLI maps it to exit code 1.
type RunError struct{ Err error }

func (e *RunError) Error() string { return e.Err.Error() }
func (e *RunError) Unwrap() error { return e.Err }

func runErrorf(format string, args ...any) error {
	return &RunError{Err: fmt.Errorf(format, args...)}
}

// IsRun reports whether err is a RunError.
func IsRun(err error) bool {
	var re *RunError
	return errors.As(err, &re)
}

// ConflictError reports that a file changed on disk since it was read, so a
// save would overwrite an external modification. Callers must surface both
// versions instead of overwriting.
type ConflictError struct {
	Path     string
	Baseline string // digest the caller read
	Disk     string // digest now on disk
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("file %q changed on disk since it was read; refusing to overwrite", e.Path)
}

// IsConflict reports whether err is a ConflictError.
func IsConflict(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
}
