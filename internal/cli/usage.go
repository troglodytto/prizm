package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// usageError marks a failure caused by *how* a command was invoked — a
// missing argument, an unknown flag, a group that could not be inferred —
// rather than by something that went wrong while running it.
//
// The distinction decides what the user sees. Invoked wrongly, they need the
// command's help; failed at runtime, help is noise that buries the actual
// error.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// errUsage builds a usage error.
func errUsage(format string, a ...any) error {
	return usageError{err: fmt.Errorf(format, a...)}
}

// usageArgs wraps a positional-argument validator so a wrong argument count
// reports as a usage error.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError{err: err}
		}
		return nil
	}
}

// Run executes the command tree, showing the failing command's help when the
// problem was how it was invoked.
func Run(root *cobra.Command) error {
	cmd, err := root.ExecuteC()
	if err == nil {
		return nil
	}

	var ue usageError
	if !errors.As(err, &ue) {
		return err
	}

	// Print the error first so it survives a long help block scrolling past.
	fmt.Fprintln(cmd.ErrOrStderr(), "Error:", err)
	fmt.Fprintln(cmd.ErrOrStderr())
	if helpErr := cmd.Help(); helpErr != nil {
		return err
	}
	return errShown{err}
}

// errShown wraps an error whose message has already been printed, so the
// caller sets a non-zero exit code without printing it twice.
type errShown struct{ err error }

func (e errShown) Error() string { return e.err.Error() }
func (e errShown) Unwrap() error { return e.err }
