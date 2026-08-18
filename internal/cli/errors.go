package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/thevalmarch/inoculum/internal/presentation"
)

type usageError struct {
	err      error
	reported bool
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}

func reportedUsageError(err error) error {
	return &usageError{err: err, reported: true}
}

type commandError struct {
	err      error
	verbose  bool
	reported bool
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func runtimeError(err error, verbose bool) error {
	if err == nil {
		return nil
	}
	return &commandError{err: err, verbose: verbose}
}

func reportedRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	return &commandError{err: err, reported: true}
}

func renderExit(err error, streams Streams) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var usage *usageError
	if errors.As(err, &usage) {
		if !usage.reported {
			fmt.Fprintln(streams.Stderr, usage.Error())
		}
		return 2
	}
	var command *commandError
	if errors.As(err, &command) {
		if !command.reported {
			fmt.Fprintln(streams.Stderr, presentation.FriendlyError(command.err, command.verbose))
		}
		return 1
	}
	fmt.Fprintln(streams.Stderr, presentation.FriendlyError(err, false))
	return 1
}
