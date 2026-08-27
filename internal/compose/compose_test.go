package compose

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fake records the command line instead of running it.
type fake struct {
	calls  [][]string
	output string
	err    error
}

func (f *fake) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.output, f.err
}

func (f *fake) last() string {
	if len(f.calls) == 0 {
		return ""
	}
	return strings.Join(f.calls[len(f.calls)-1], " ")
}

func stack() Stack {
	return Stack{ComposePath: "qa.yml", Services: []string{"db-tunnel"}, Project: "prizm-k-qa"}
}

func TestUpNamesTheFileProjectAndServices(t *testing.T) {
	f := &fake{}
	if _, err := Up(context.Background(), f, stack()); err != nil {
		t.Fatalf("up: %v", err)
	}

	got := f.last()
	for _, want := range []string{"-f qa.yml", "--project-name prizm-k-qa", "up -d", "db-tunnel"} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to contain %q", got, want)
		}
	}
}

func TestDownStopsNamedServicesRatherThanTheProject(t *testing.T) {
	f := &fake{}
	if _, err := Down(context.Background(), f, stack()); err != nil {
		t.Fatalf("down: %v", err)
	}

	got := f.last()
	if !strings.Contains(got, "stop db-tunnel") {
		t.Errorf("command = %q, want `stop <service>`", got)
	}
	if strings.Contains(got, " down") {
		t.Errorf("command = %q, want no `down` — it would take services another workflow started", got)
	}
}

func TestDownWithoutNamedServicesTakesTheProject(t *testing.T) {
	f := &fake{}
	s := stack()
	s.Services = nil

	if _, err := Down(context.Background(), f, s); err != nil {
		t.Fatalf("down: %v", err)
	}
	if !strings.Contains(f.last(), "down") {
		t.Errorf("command = %q, want `down` when the workflow owns the whole file", f.last())
	}
}

func TestRunningFiltersToTheWorkflowsServices(t *testing.T) {
	f := &fake{output: "db-tunnel\nmongo\n\n"}

	got, _, err := Running(context.Background(), f, stack())
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	if len(got) != 1 || got[0] != "db-tunnel" {
		t.Errorf("running = %v, want only the services this workflow declared", got)
	}
}

func TestRunningReturnsDockersOutputOnFailure(t *testing.T) {
	f := &fake{output: "cannot connect to the docker daemon", err: errors.New("exit status 1")}

	// The caller reports docker's words, not prizm's — "exit status 1" sends
	// someone to debug the wrong tool.
	if _, out, err := Running(context.Background(), f, stack()); err == nil || out == "" {
		t.Errorf("out=%q err=%v, want both the failure and docker's explanation", out, err)
	}
}

func TestRunningPropagatesFailure(t *testing.T) {
	f := &fake{err: errors.New("daemon not running")}
	if _, _, err := Running(context.Background(), f, stack()); err == nil {
		t.Error("want the docker failure surfaced, not an empty list")
	}
}

func TestProjectNameSeparatesWorkflowsSharingAComposeFile(t *testing.T) {
	if a, b := ProjectName("k", "local"), ProjectName("k", "staging"); a == b {
		t.Fatal("two workflows must not share a project name")
	}
	if got := ProjectName("Acme Ltd", "QA/Stage"); got != "prizm-acme-ltd-qa-stage" {
		t.Errorf("ProjectName = %q, want it reduced to compose-legal characters", got)
	}
}
