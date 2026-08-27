package cli

import (
	"fmt"
	"io"

	"github.com/troglodytto/prizm/internal/style"
)

// This file is prizm's only writer to stdout. Every command prints through
// these helpers, and a guard test (output_test.go) fails the build if a
// fmt.Fprint call appears anywhere else in the package.
//
// The point is not ceremony. Styling applied at each call site is styling
// that gets forgotten at the next one, and the result is a tool that looks
// half-finished in exactly the places nobody demoed. One seam means the
// question "is this styled?" has one answer.

// say prints a line that is already styled, or needs no styling.
func (a *App) say(s string) { fmt.Fprintln(a.Out, s) }

// sayf prints styled fragments assembled with a format.
func (a *App) sayf(format string, args ...any) { fmt.Fprintf(a.Out, format+"\n", args...) }

// blank separates blocks.
func (a *App) blank() { fmt.Fprintln(a.Out) }

// row prints a status line in a measured column.
func (a *App) row(c style.Column, m style.Mark, name, detail string) {
	fmt.Fprintln(a.Out, c.Row(m, name, detail))
}

// result prints a one-off status line with nothing to align against.
func (a *App) result(m style.Mark, name, detail string) {
	fmt.Fprintln(a.Out, style.Row(m, name, detail))
}

// field prints a name/value line inside a listing.
func (a *App) field(c style.Column, name, detail string) {
	fmt.Fprintln(a.Out, c.Field(name, detail))
}

// heading names the subject of a block.
func (a *App) heading(format string, args ...any) {
	fmt.Fprintln(a.Out, style.Heading(fmt.Sprintf(format, args...)))
}

// section labels a block within a listing.
func (a *App) section(indent, label string) {
	fmt.Fprintln(a.Out, indent+style.Section(label))
}

// detail prints secondary text: a path, a value, a count.
func (a *App) detail(format string, args ...any) {
	fmt.Fprintln(a.Out, style.Detail(fmt.Sprintf(format, args...)))
}

// hint points at the next command to run.
func (a *App) hint(format string, args ...any) {
	fmt.Fprintln(a.Out, style.Hint(fmt.Sprintf(format, args...)))
}

// prompt writes a confirmation question, which stays on its own line so the
// answer types beside it.
func (a *App) prompt(s string) { fmt.Fprint(a.Out, s) }

// Error output goes to stderr, and through here for the same reason as
// everything else: an unstyled "Error:" is the one line a user most needs to
// find in a scrollback.

// failLine writes a styled error line to w.
func failLine(w io.Writer, err error) { fmt.Fprintln(w, style.ErrorLabel(), err) }

// blankLine separates the error from whatever follows it.
func blankLine(w io.Writer) { fmt.Fprintln(w) }

// promptLine writes a question that the answer is typed beside, so it must
// not end in a newline.
func promptLine(w io.Writer, s string) { fmt.Fprint(w, s) }
