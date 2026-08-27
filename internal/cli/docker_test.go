package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/compose"
)

// fakeDocker records what would have been run.
type fakeDocker struct {
	calls  []string
	output string
	err    error
}

func (f *fakeDocker) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	return f.output, f.err
}

func (f *fakeDocker) saw(fragment string) bool {
	for _, call := range f.calls {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

// seedDocker returns the repo directory as well: repoDir mints a fresh temp
// dir per call, so an assertion that calls it again looks in the wrong place.
func (h *harness) seedDocker(t *testing.T) (composeFile, repoDir string, docker *fakeDocker) {
	t.Helper()

	composeFile = filepath.Join(t.TempDir(), "stack.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  db: {image: postgres}\n"), 0o600); err != nil {
		t.Fatalf("writing compose file: %v", err)
	}

	docker = &fakeDocker{}
	h.app.Docker = docker
	repoDir = h.repoDir(t, "auth")

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", repoDir)
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")
	h.run(t, "var", "k", "auth", "PORT=4000")
	return composeFile, repoDir, docker
}

func TestDockerAttachRejectsAMissingFile(t *testing.T) {
	h := newHarness(t)
	_, _, _ = h.seedDocker(t)

	if err := h.run(t, "docker", "k", "local", "--compose", "/nope/stack.yml"); err == nil {
		t.Error("want a refusal at attach time, not a surprise at the next up")
	}
}

func TestDockerAttachThenShow(t *testing.T) {
	h := newHarness(t)
	file, _, _ := h.seedDocker(t)

	if err := h.run(t, "docker", "k", "local", "--compose", file, "--services", "db"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := h.run(t, "docker", "k", "local"); err != nil {
		t.Fatalf("show: %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "db") || !strings.Contains(out, "prizm-k-local") {
		t.Errorf("output = %q, want the services and the project name", out)
	}
}

func TestUpStartsTheStackAfterWritingEnvFiles(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file, "--services", "db")

	if err := h.run(t, "up", "k", "local"); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !docker.saw("up -d") {
		t.Errorf("docker calls = %v, want the stack started", docker.calls)
	}
	if !docker.saw("--project-name prizm-k-local") {
		t.Errorf("docker calls = %v, want the workflow's own project", docker.calls)
	}
}

func TestDockerFailureDoesNotFailTheEnvWrite(t *testing.T) {
	h := newHarness(t)
	file, repoDir, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	docker.err = compose.ErrNoDocker

	// The whole point: env files are the reliable half and must not be held
	// hostage by the daemon.
	if err := h.run(t, "up", "k", "local"); err != nil {
		t.Fatalf("up = %v, want success — docker is best-effort", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".env")); err != nil {
		t.Errorf("env file missing: %v", err)
	}
	if !strings.Contains(h.out.String(), "only affects the services") {
		t.Errorf("output = %q, want the failure scoped to docker", h.out.String())
	}
}

func TestPartialApplyDoesNotStartServices(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	h.run(t, "var", "k", "auth", "BROKEN=${_PRIZM_MISSING}")

	if err := h.run(t, "up", "k", "local"); err == nil {
		t.Fatal("want the apply to fail")
	}
	if docker.saw("up -d") {
		t.Errorf("docker calls = %v, want no services for repos that failed to configure", docker.calls)
	}
}

func TestDownStopsTheStack(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file, "--services", "db")

	if err := h.run(t, "down", "k", "local"); err != nil {
		t.Fatalf("down: %v", err)
	}
	if !docker.saw("stop db") {
		t.Errorf("docker calls = %v, want the named service stopped", docker.calls)
	}
}

func TestDownWithoutAStackSaysSo(t *testing.T) {
	h := newHarness(t)
	_, _, _ = h.seedDocker(t)

	if err := h.run(t, "down", "k", "local"); err != nil {
		t.Fatalf("down = %v, want a hint rather than an error", err)
	}
	if !strings.Contains(h.out.String(), "nothing to stop") {
		t.Errorf("output = %q, want it to explain there is no stack", h.out.String())
	}
}

func TestDownLeavesEnvFilesAlone(t *testing.T) {
	h := newHarness(t)
	file, repoDir, _ := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	h.run(t, "up", "k", "local")
	h.run(t, "down", "k", "local")

	raw, err := os.ReadFile(filepath.Join(repoDir, ".env"))
	if err != nil {
		t.Fatalf("reading env: %v", err)
	}
	if !strings.Contains(string(raw), "PORT=4000") {
		t.Error("down cleared the env file — it stops containers, not configuration")
	}
}

func TestDetachLeavesContainersRunning(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	docker.calls = nil

	if err := h.run(t, "docker", "k", "local", "--detach"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if len(docker.calls) != 0 {
		t.Errorf("docker calls = %v, want none — detach edits config, it does not stop anything", docker.calls)
	}
	if !strings.Contains(h.out.String(), "prizm down") {
		t.Errorf("output = %q, want it to name the command that does stop them", h.out.String())
	}
}

func TestServicesWithoutComposeIsRefused(t *testing.T) {
	h := newHarness(t)
	_, _, _ = h.seedDocker(t)

	if err := h.run(t, "docker", "k", "local", "--services", "db"); err == nil {
		t.Error("want a refusal: there is no file to take those services from")
	}
}

func TestShowSurvivesAnUnreachableDaemon(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	docker.err = errors.New("cannot connect to the docker daemon")

	if err := h.run(t, "docker", "k", "local"); err != nil {
		t.Fatalf("show = %v, want the listing to survive", err)
	}
	if !strings.Contains(h.out.String(), "unknown") {
		t.Errorf("output = %q, want the running column to say it could not tell", h.out.String())
	}
}

func TestStatusShowsRunningServices(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file, "--services", "db")
	h.run(t, "up", "k", "local")

	docker.output = "db\n"
	if err := h.run(t, "status", "k"); err != nil {
		t.Fatalf("status: %v", err)
	}

	// Without this, `prizm down` is a command nobody discovers.
	if !strings.Contains(h.out.String(), "services running") {
		t.Errorf("output = %q, want the running services listed", h.out.String())
	}
}

func TestStatusSurvivesAnUnreachableDaemon(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)
	h.run(t, "up", "k", "local")
	docker.err = compose.ErrNoDocker

	if err := h.run(t, "status", "k"); err != nil {
		t.Fatalf("status = %v, want the repo listing to survive", err)
	}
	if !strings.Contains(h.out.String(), "services unknown") {
		t.Errorf("output = %q, want it to say it could not tell", h.out.String())
	}
}

func TestFailuresReportDockersOwnWords(t *testing.T) {
	h := newHarness(t)
	file, _, docker := h.seedDocker(t)
	h.run(t, "docker", "k", "local", "--compose", file)

	docker.output = "Container prizm-k-local-db-1  Creating\n" +
		"cannot connect to the docker daemon at unix:///var/run/docker.sock\n"
	docker.err = errors.New("exit status 1")

	h.run(t, "up", "k", "local")

	out := h.out.String()
	if !strings.Contains(out, "cannot connect to the docker daemon") {
		t.Errorf("output = %q, want docker's explanation", out)
	}
	if strings.Contains(out, "exit status 1") {
		t.Errorf("output = %q, want the reason rather than the exit code", out)
	}
	if strings.Contains(out, "Creating") {
		t.Errorf("output = %q, want progress chatter skipped in favour of the failure", out)
	}
}
