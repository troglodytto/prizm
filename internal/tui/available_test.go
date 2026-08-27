package tui

import "testing"

func reset() { disabled = false }

// Tests never run attached to a terminal, so this is also the no-TTY case.
func TestAvailableIsFalseWithoutATerminal(t *testing.T) {
	if Available() {
		t.Error("Available() = true in a test process, want false")
	}
}

func TestDisableTurnsItOff(t *testing.T) {
	Disable()
	t.Cleanup(reset)

	if Available() {
		t.Error("Available() = true after Disable(), want false")
	}
}

func TestEnvOverrideTurnsItOff(t *testing.T) {
	t.Setenv("PRIZM_NO_TUI", "1")
	t.Cleanup(reset)

	if Available() {
		t.Error("Available() = true with PRIZM_NO_TUI set, want false")
	}
}
