package tui

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// frames is the braille spinner. It is inlined rather than pulled from
// bubbles: ten runes and a ticker do not justify a dependency tree, and the
// one bubbles brings would upgrade the renderer underneath every other view.
var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const frameRate = 100 * time.Millisecond

type tickMsg struct{}

// doneMsg carries the worker's result back into the model.
type doneMsg struct {
	out string
	err error
}

type spinnerModel struct {
	label string
	frame int
	out   string
	err   error
	done  bool
}

func newSpinnerModel(label string) spinnerModel { return spinnerModel{label: label} }

func tick() tea.Cmd {
	return tea.Tick(frameRate, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m spinnerModel) Init() tea.Cmd { return tick() }

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		m.out, m.err, m.done = msg.out, msg.err, true
		return m, tea.Quit
	case tickMsg:
		m.frame = (m.frame + 1) % len(frames)
		return m, tick()
	}
	// Keys are ignored on purpose: quitting here would leave the work
	// running with nothing watching it. Ctrl-C cancels the context instead.
	return m, nil
}

func (m spinnerModel) View() string {
	if m.done {
		return ""
	}
	return "  " + barStyle.Render(frames[m.frame]) + " " + dimStyle.Render(m.label)
}

// Spin runs work while showing a spinner, and returns whatever work returns.
//
// The work runs on its own goroutine feeding a message back, which is what
// keeps the spinner animating instead of freezing on the first slow call.
// With no terminal the work simply runs, so a script sees no difference.
func Spin(ctx context.Context, label string, work func(context.Context) (string, error)) (string, error) {
	if !Available() {
		return work(ctx)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	program := tea.NewProgram(newSpinnerModel(label), tea.WithOutput(os.Stderr), tea.WithContext(ctx))

	go func() {
		out, err := work(ctx)
		program.Send(doneMsg{out: out, err: err})
	}()

	final, err := program.Run()
	if err != nil {
		// A failed spinner is not failed work. Reporting a UI problem as a
		// docker problem would send someone debugging the wrong thing.
		return work(context.WithoutCancel(ctx))
	}

	model, ok := final.(spinnerModel)
	if !ok {
		return "", nil
	}
	return model.out, model.err
}
