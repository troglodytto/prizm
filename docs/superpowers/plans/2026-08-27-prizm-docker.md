# prizm Phase 5 — Docker & Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a workflow declare supporting services — the spec's DB tunnel is the motivating case — bring them up as part of `prizm up`, tear them down with `prizm down`, and show them in `prizm status`, without ever letting Docker's failures touch the env-application path.

**Architecture:** An `internal/dockerctl` package wrapping the `docker compose` **CLI**, behind a `Runner` interface so every test asserts on the exact argv rather than starting containers. Compose attachments are stored per `(workflow)`, matching the spec's "shared infrastructure for the whole environment" framing. `up` applies env files first and *then* starts services, reporting the two phases separately, so a stopped Docker daemon can never corrupt or block a symlink swap.

**Tech Stack:** No new Go dependencies. Requires the `docker` CLI with the Compose v2 plugin at runtime; its absence is a warning, not an error.

**Spec:** The transcript's Docker section — compose scoped to the environment rather than the repo, shelling out to the CLI rather than the SDK, best-effort and clearly separated from env setting, a spinner while containers start, and `prizm down` as the eventual inverse.

**Prerequisite:** Phases 1–4 complete.

## Global Constraints

- All earlier constraints hold.
- **Shell out to `docker compose`.** Not the Go SDK. It is far less code and it behaves exactly like what the user would type by hand, which is what makes it debuggable when it breaks — the spec's reasoning, adopted.
- **Docker is best-effort and clearly separated.** Env files are applied first and reported first. A Docker failure is printed as its own line and sets a non-zero exit, but it never prevents, delays, or rolls back env application.
- **No container work in `--dry-run`**, and none on the completion path.
- **One compose project per workflow**, named `prizm-<group>-<workflow>`. Without this, two workflows sharing a compose file would tear down each other's containers.
- **Tests never invoke Docker.** Every test injects a fake `Runner` and asserts the command line.
- **All output goes through `internal/style`.** A started service and an applied repo are the same kind of line and must look it.

---

## Design: Two Decisions the Spec Left Open

**1. What happens to the old workflow's containers when you switch?**

The spec asked this and did not answer it. The options are auto-teardown or leave-running, and both have a real failure mode: auto-teardown kills the tunnel you were debugging through in another terminal; leave-running means a `qa` tunnel can still be up while you are pointed at `production`, which is the more dangerous of the two.

prizm does neither silently. **Containers are left running, and `up` warns about the ones that no longer belong**:

```
$ prizm XYZ up production
✓ frontend   set (production)
✓ backend    set (production)
✓ services   db-tunnel started
⚠ XYZ/qa still has services running — `prizm down XYZ qa` to stop them
```

This is the same rule the rest of prizm follows: report, point at the fix, never act on the user's behalf. It keeps `up` fast and non-destructive, and it removes the "wrong tunnel is still up" hazard by making it visible rather than by guessing. Flipping to auto-teardown later is a one-line change in `applyWorkflow` — but it would be a change of policy, not a bug fix.

**2. Where are services attached?**

Per `(group, workflow)`, never per repo. The spec's example — a DB tunnel shared by backend, auth and ai — is infrastructure for the whole workflow, and attaching it to a repo would mean deciding which of three equal consumers "owns" it.

---

### Task 1: Compose attachments

**Files:**
- Create: `internal/dockerctl/compose.go`, `internal/store/docker.go`, `internal/cli/docker.go`
- Modify: `internal/store/migrate.go` (migration 3), `internal/cli/root.go` (register)
- Test: `internal/dockerctl/compose_test.go`, `internal/cli/docker_test.go`

**Interfaces:**
- Produces:
  - `dockerctl.Runner` interface: `Run(name string, args ...string) (string, error)`.
  - `dockerctl.Exec` — the real runner.
  - `dockerctl.Compose` struct: `Runner Runner`; methods `Up(project, file string, services []string) error`, `Down(project, file string) error`, `PS(project, file string) ([]Service, error)`, `Available() bool`.
  - `dockerctl.Service` struct: `Name string`, `State string`.
  - `dockerctl.ProjectName(group, workflow string) string` → `prizm-<group>-<workflow>`, lowercased and sanitised.
  - `store.Attachment` struct: `WorkflowID int64`, `ComposeFile string`, `Services []string`.
  - `store.(*Store).SetAttachment(workflowID int64, composeFile string, services []string) error`, `AttachmentFor(workflowID int64) (Attachment, bool, error)`, `AttachmentsIn(groupID int64) (map[int64]Attachment, error)`.
  - `prizm docker-add [group] <workflow> --compose FILE [--services a,b]`, `prizm docker-ls [group]`.

- [ ] **Step 1: Write the failing compose test**

Create `internal/dockerctl/compose_test.go`:

```go
package dockerctl

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// fakeRunner records invocations and returns scripted output.
type fakeRunner struct {
	calls  [][]string
	output string
	err    error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.output, f.err
}

func TestUpBuildsTheExpectedCommand(t *testing.T) {
	r := &fakeRunner{}
	c := Compose{Runner: r}

	if err := c.Up("prizm-xyz-local", "/infra/qa.yml", []string{"db-tunnel"}); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	want := []string{"docker", "compose", "-p", "prizm-xyz-local", "-f", "/infra/qa.yml", "up", "-d", "db-tunnel"}
	if diff := cmp.Diff(want, r.calls[0]); diff != "" {
		t.Errorf("command mismatch (-want +got):\n%s", diff)
	}
}

func TestUpWithNoServicesStartsEverything(t *testing.T) {
	r := &fakeRunner{}
	c := Compose{Runner: r}

	c.Up("prizm-xyz-local", "/infra/qa.yml", nil)

	want := []string{"docker", "compose", "-p", "prizm-xyz-local", "-f", "/infra/qa.yml", "up", "-d"}
	if diff := cmp.Diff(want, r.calls[0]); diff != "" {
		t.Errorf("command mismatch (-want +got):\n%s", diff)
	}
}

func TestUpSurfacesTheCommandOutputOnFailure(t *testing.T) {
	r := &fakeRunner{output: "no configuration file provided", err: errors.New("exit 1")}
	c := Compose{Runner: r}

	err := c.Up("prizm-xyz-local", "/infra/qa.yml", nil)
	if err == nil {
		t.Fatal("Up() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no configuration file provided") {
		t.Errorf("error = %q, want it to include docker's own message", err)
	}
}

func TestDownBuildsTheExpectedCommand(t *testing.T) {
	r := &fakeRunner{}
	c := Compose{Runner: r}

	if err := c.Down("prizm-xyz-local", "/infra/qa.yml"); err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	want := []string{"docker", "compose", "-p", "prizm-xyz-local", "-f", "/infra/qa.yml", "down"}
	if diff := cmp.Diff(want, r.calls[0]); diff != "" {
		t.Errorf("command mismatch (-want +got):\n%s", diff)
	}
}

func TestPSParsesJSONLines(t *testing.T) {
	r := &fakeRunner{output: `{"Name":"prizm-xyz-local-db-tunnel-1","Service":"db-tunnel","State":"running"}
{"Name":"prizm-xyz-local-cache-1","Service":"cache","State":"exited"}`}
	c := Compose{Runner: r}

	got, err := c.PS("prizm-xyz-local", "/infra/qa.yml")
	if err != nil {
		t.Fatalf("PS() error = %v", err)
	}

	want := []Service{
		{Name: "db-tunnel", State: "running"},
		{Name: "cache", State: "exited"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("PS() mismatch (-want +got):\n%s", diff)
	}
}

func TestPSWithNoContainersIsEmptyNotAnError(t *testing.T) {
	r := &fakeRunner{output: "\n"}
	c := Compose{Runner: r}

	got, err := c.PS("prizm-xyz-local", "/infra/qa.yml")
	if err != nil {
		t.Fatalf("PS() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("PS() = %v, want empty", got)
	}
}

// A stopped daemon must read as "nothing running", never as a hard failure.
func TestPSTreatsARunnerFailureAsNothingRunning(t *testing.T) {
	r := &fakeRunner{err: errors.New("cannot connect to the Docker daemon")}
	c := Compose{Runner: r}

	got, err := c.PS("prizm-xyz-local", "/infra/qa.yml")
	if err != nil {
		t.Errorf("PS() error = %v, want nil — a stopped daemon is not a prizm error", err)
	}
	if len(got) != 0 {
		t.Errorf("PS() = %v, want empty", got)
	}
}

func TestProjectNameIsSanitised(t *testing.T) {
	tests := map[string]string{
		"XYZ/local":            "prizm-xyz-local",
		"My Group/QA Full":     "prizm-my-group-qa-full",
		"a_b/c.d":              "prizm-a_b-c-d",
	}

	for in, want := range tests {
		parts := strings.SplitN(in, "/", 2)
		if got := ProjectName(parts[0], parts[1]); got != want {
			t.Errorf("ProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/dockerctl/`
Expected: FAIL — `undefined: Compose`.

- [ ] **Step 3: Implement the compose wrapper**

Create `internal/dockerctl/compose.go`:

```go
// Package dockerctl wraps the docker compose CLI. prizm shells out rather than
// using the SDK so its behaviour matches what a user would type by hand.
package dockerctl

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Runner executes a command and returns its combined output.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// Exec is the real runner.
type Exec struct{}

func (Exec) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Service is one compose service's state.
type Service struct {
	Name  string
	State string
}

// Compose drives `docker compose` for one project at a time.
type Compose struct {
	Runner Runner
}

// Available reports whether the docker CLI with the compose plugin is usable.
func (c Compose) Available() bool {
	_, err := c.Runner.Run("docker", "compose", "version")
	return err == nil
}

// Up starts the given services detached, or every service when none are named.
func (c Compose) Up(project, file string, services []string) error {
	args := append(baseArgs(project, file), "up", "-d")
	args = append(args, services...)

	out, err := c.Runner.Run("docker", args...)
	if err != nil {
		return fmt.Errorf("docker compose up: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Down stops and removes the project's containers.
func (c Compose) Down(project, file string) error {
	out, err := c.Runner.Run("docker", append(baseArgs(project, file), "down")...)
	if err != nil {
		return fmt.Errorf("docker compose down: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// PS lists the project's services. A failure — a stopped daemon, a missing
// compose file — reads as "nothing running" rather than as a prizm error, because
// status must never fail just because Docker is not up.
func (c Compose) PS(project, file string) ([]Service, error) {
	out, err := c.Runner.Run("docker", append(baseArgs(project, file), "ps", "--format", "json")...)
	if err != nil {
		return nil, nil
	}

	var services []Service
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var row struct {
			Service string `json:"Service"`
			State   string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue // tolerate format drift rather than failing status
		}
		services = append(services, Service{Name: row.Service, State: row.State})
	}
	return services, nil
}

func baseArgs(project, file string) []string {
	return []string{"compose", "-p", project, "-f", file}
}

var unsafeProjectChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// ProjectName isolates each workflow's containers. Without a per-workflow
// project, two workflows sharing a compose file would tear down each other's
// containers.
func ProjectName(group, workflow string) string {
	clean := func(s string) string {
		return strings.Trim(unsafeProjectChars.ReplaceAllString(strings.ToLower(s), "-"), "-")
	}
	return "prizm-" + clean(group) + "-" + clean(workflow)
}
```

- [ ] **Step 4: Add the schema and store methods**

Append migration 3 to `migrations` in `internal/store/migrate.go`:

```go
	// 3: compose attachments and which workflow last started services.
	`
CREATE TABLE IF NOT EXISTS docker_attachments (
	workflow_id  INTEGER PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
	compose_file TEXT    NOT NULL,
	services     TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS docker_started (
	workflow_id INTEGER PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
	started_at  INTEGER NOT NULL
);
`,
```

Create `internal/store/docker.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Attachment is a workflow's compose configuration.
type Attachment struct {
	WorkflowID  int64
	ComposeFile string
	Services    []string
}

// SetAttachment attaches (or re-attaches) a compose file to a workflow.
func (s *Store) SetAttachment(workflowID int64, composeFile string, services []string) error {
	_, err := s.db.Exec(`
		INSERT INTO docker_attachments(workflow_id, compose_file, services, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(workflow_id) DO UPDATE SET
			compose_file = excluded.compose_file, services = excluded.services`,
		workflowID, composeFile, strings.Join(services, ","), time.Now().Unix())
	return err
}

// AttachmentFor returns a workflow's compose configuration, if it has one.
func (s *Store) AttachmentFor(workflowID int64) (Attachment, bool, error) {
	var (
		a   Attachment
		raw string
	)
	err := s.db.QueryRow(
		`SELECT workflow_id, compose_file, services FROM docker_attachments WHERE workflow_id = ?`,
		workflowID,
	).Scan(&a.WorkflowID, &a.ComposeFile, &raw)

	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, false, nil
	}
	if err != nil {
		return Attachment{}, false, err
	}
	a.Services = splitServices(raw)
	return a, true, nil
}

// AttachmentsIn returns every attachment in a group, keyed by workflow ID.
func (s *Store) AttachmentsIn(groupID int64) (map[int64]Attachment, error) {
	rows, err := s.db.Query(`
		SELECT d.workflow_id, d.compose_file, d.services
		FROM docker_attachments d
		JOIN workflows w ON w.id = d.workflow_id
		WHERE w.group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]Attachment)
	for rows.Next() {
		var (
			a   Attachment
			raw string
		)
		if err := rows.Scan(&a.WorkflowID, &a.ComposeFile, &raw); err != nil {
			return nil, err
		}
		a.Services = splitServices(raw)
		out[a.WorkflowID] = a
	}
	return out, rows.Err()
}

// MarkStarted records that a workflow's services were brought up.
func (s *Store) MarkStarted(workflowID int64, now time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO docker_started(workflow_id, started_at) VALUES (?, ?)
		ON CONFLICT(workflow_id) DO UPDATE SET started_at = excluded.started_at`,
		workflowID, now.Unix())
	return err
}

// MarkStopped clears that record.
func (s *Store) MarkStopped(workflowID int64) error {
	_, err := s.db.Exec(`DELETE FROM docker_started WHERE workflow_id = ?`, workflowID)
	return err
}

// StartedWorkflows returns the workflows in a group whose services prizm started.
func (s *Store) StartedWorkflows(groupID int64) (map[int64]bool, error) {
	rows, err := s.db.Query(`
		SELECT d.workflow_id
		FROM docker_started d
		JOIN workflows w ON w.id = d.workflow_id
		WHERE w.group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func splitServices(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}
```

- [ ] **Step 5: Implement `docker-add` and `docker-ls`**

Create `internal/cli/docker.go`:

```go
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/dockerctl"
)

func newDockerAddCmd(app *App) *cobra.Command {
	var (
		compose  string
		services string
	)

	cmd := &cobra.Command{
		Use:   "docker-add [group] <workflow>",
		Short: "Attach a compose file to a workflow",
		Long: "Services are attached to a workflow, not a repo: a database tunnel shared\n" +
			"by three services is infrastructure for the whole workflow.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}
			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}

			if compose == "" {
				return fmt.Errorf("--compose is required")
			}
			abs, err := resolvePath(app, compose)
			if err != nil {
				return err
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("compose file %s is not readable: %w", abs, err)
			}

			var list []string
			for _, s := range strings.Split(services, ",") {
				if s = strings.TrimSpace(s); s != "" {
					list = append(list, s)
				}
			}

			if err := app.Store.SetAttachment(wf.ID, abs, list); err != nil {
				return err
			}

			fmt.Fprintf(app.Out, "attached %s to %s/%s (project %s)\n",
				abs, g.Name, wf.Name, dockerctl.ProjectName(g.Name, wf.Name))
			return nil
		},
	}

	cmd.Flags().StringVar(&compose, "compose", "", "path to a docker compose file")
	cmd.Flags().StringVar(&services, "services", "", "comma-separated subset of services to start (default: all)")
	return cmd
}

func newDockerLsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "docker-ls [group]",
		Short: "List workflows with attached compose files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}

			attachments, err := app.Store.AttachmentsIn(g.ID)
			if err != nil {
				return err
			}
			if len(attachments) == 0 {
				fmt.Fprintf(app.Out, "%s has no compose attachments\n", g.Name)
				return nil
			}

			workflows, err := app.Store.ListWorkflows(g.ID)
			if err != nil {
				return err
			}
			for _, w := range workflows {
				a, ok := attachments[w.ID]
				if !ok {
					continue
				}
				services := "(all)"
				if len(a.Services) > 0 {
					services = strings.Join(a.Services, ", ")
				}
				fmt.Fprintf(app.Out, "  %-16s %s\n    services: %s\n", w.Name, a.ComposeFile, services)
			}
			return nil
		},
	}
}
```

Register both commands, and add `"docker-add"`, `"docker-ls"`, `"down"` to `reservedNames` in `internal/store/workflows.go`.

- [ ] **Step 6: Write and run the command test**

Create `internal/cli/docker_test.go` covering: `docker-add` stores an absolute compose path and the service subset; it rejects a missing `--compose`; it rejects a compose file that does not exist; `docker-ls` prints the attachment; and `add-workflow` refuses the name `down`.

Run: `go test ./internal/dockerctl/ ./internal/cli/ ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/dockerctl/ internal/store/ internal/cli/
git commit -m "feat(docker): attach compose files to workflows"
```

---

### Task 2: `up` starts services

**Files:**
- Modify: `internal/cli/up.go`, `internal/cli/root.go` (`App.Compose`)
- Test: `internal/cli/up_docker_test.go`

**Interfaces:**
- Produces:
  - `App.Compose dockerctl.Compose` — injectable, defaulting to `dockerctl.Compose{Runner: dockerctl.Exec{}}`.
  - `prizm up ... --no-docker`
  - `cli.startServices(app *App, g store.Group, wf store.Workflow) error`

Order is the whole point: **env files first, then services.** Every repo is reported before Docker is touched, so a stopped daemon produces a clearly separated failure line under a successful env application rather than an ambiguous half-failure.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/up_docker_test.go`:

```go
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/dockerctl"
)

type fakeRunner struct {
	calls [][]string
	err   error
	out   string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out, f.err
}

func (f *fakeRunner) sawUp() bool {
	for _, call := range f.calls {
		for _, arg := range call {
			if arg == "up" {
				return true
			}
		}
	}
	return false
}

func (h *harness) seedDocker(t *testing.T) (string, *fakeRunner) {
	t.Helper()

	beDir := h.repoDir(t, "backend")
	composeFile := filepath.Join(t.TempDir(), "qa.yml")
	os.WriteFile(composeFile, []byte("services: {}\n"), 0o644)

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "docker-add", "XYZ", "local", "--compose", composeFile, "--services", "db-tunnel")

	r := &fakeRunner{}
	h.app.Compose = dockerctl.Compose{Runner: r}
	return beDir, r
}

func TestUpStartsAttachedServices(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v", err)
	}
	if !r.sawUp() {
		t.Fatalf("docker compose up was never called; calls = %v", r.calls)
	}

	joined := strings.Join(r.calls[len(r.calls)-1], " ")
	if !strings.Contains(joined, "prizm-xyz-local") {
		t.Errorf("command = %q, want the per-workflow project name", joined)
	}
	if !strings.Contains(joined, "db-tunnel") {
		t.Errorf("command = %q, want the attached service subset", joined)
	}
}

// The core separation guarantee.
func TestUpAppliesEnvEvenWhenDockerFails(t *testing.T) {
	h := newHarness(t)
	beDir, r := h.seedDocker(t)
	r.err = errors.New("cannot connect to the Docker daemon")

	err := h.run(t, "up", "XYZ", "local")
	if err == nil {
		t.Fatal("up error = nil, want a non-zero result when services failed")
	}

	body, readErr := os.ReadFile(filepath.Join(beDir, ".env"))
	if readErr != nil {
		t.Fatalf("env file was not written: %v", readErr)
	}
	if string(body) != "A=1\n" {
		t.Errorf("env = %q, want it applied despite the Docker failure", body)
	}

	out := h.out.String()
	if strings.Index(out, "backend") > strings.Index(out, "services") {
		t.Error("Docker was reported before the env files; env must come first")
	}
}

func TestUpWithoutAnAttachmentTouchesDocker(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	r := &fakeRunner{}
	h.app.Compose = dockerctl.Compose{Runner: r}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")
	h.run(t, "up", "XYZ", "local")

	if len(r.calls) != 0 {
		t.Errorf("docker was invoked %v for a workflow with no attachment", r.calls)
	}
}

func TestUpNoDockerFlagSkipsServices(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)

	if err := h.run(t, "up", "XYZ", "local", "--no-docker"); err != nil {
		t.Fatalf("up error = %v", err)
	}
	if r.sawUp() {
		t.Errorf("docker was invoked despite --no-docker: %v", r.calls)
	}
}

func TestUpDryRunSkipsServices(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)

	if err := h.run(t, "up", "XYZ", "local", "--dry-run"); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("docker was invoked during a dry run: %v", r.calls)
	}
}

func TestUpRecordsThatServicesStarted(t *testing.T) {
	h := newHarness(t)
	h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	started, _ := h.app.Store.StartedWorkflows(g.ID)
	if !started[wf.ID] {
		t.Error("up did not record that services were started")
	}
}

func TestUpWarnsAboutAnotherWorkflowsRunningServices(t *testing.T) {
	h := newHarness(t)
	composeFile := filepath.Join(t.TempDir(), "qa.yml")
	os.WriteFile(composeFile, []byte("services: {}\n"), 0o644)

	_, r := h.seedDocker(t)
	h.run(t, "add-workflow", "XYZ", "production", "--repos", "backend", "--tag", "prod")
	h.run(t, "docker-add", "XYZ", "production", "--compose", composeFile)

	h.run(t, "up", "XYZ", "local")
	r.calls = nil
	h.out.Reset()
	h.run(t, "up", "XYZ", "production", "--yes")

	out := h.out.String()
	if !strings.Contains(out, "local") || !strings.Contains(out, "down") {
		t.Errorf("output = %q, want a warning naming the still-running workflow and `prizm down`", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestUpStarts|TestUpApplies'`
Expected: FAIL — `h.app.Compose undefined`.

- [ ] **Step 3: Implement service start-up**

Add to `App`: `Compose dockerctl.Compose`, defaulted in `Execute` to `dockerctl.Compose{Runner: dockerctl.Exec{}}`.

Add `--no-docker` to `up`, and at the end of `applyWorkflow` — **after** the repo loop and only when `!dryRun && !noDocker`:

```go
	if err := startServices(app, g, wf); err != nil {
		fmt.Fprintln(app.Out, style.Row(style.Fail, "services", err.Error()))
		failed++
	}
	warnOtherRunningWorkflows(app, g, wf)
```

Add both helpers to `internal/cli/up.go`:

```go
// startServices brings up a workflow's attached compose services. It runs
// after every env file is written, so Docker can never block or corrupt the
// part of `up` that matters most.
func startServices(app *App, g store.Group, wf store.Workflow) error {
	attachment, ok, err := app.Store.AttachmentFor(wf.ID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	project := dockerctl.ProjectName(g.Name, wf.Name)
	if err := app.Compose.Up(project, attachment.ComposeFile, attachment.Services); err != nil {
		return err
	}

	names := "all services"
	if len(attachment.Services) > 0 {
		names = strings.Join(attachment.Services, ", ")
	}
	fmt.Fprintln(app.Out, style.Row(style.OK, "services", names+" started"))

	return app.Store.MarkStarted(wf.ID, app.Now())
}

// warnOtherRunningWorkflows reports containers from a workflow you are no
// longer on. prizm does not stop them: the spec's rule is to report and point at
// the fix rather than act. A stale qa tunnel is dangerous when it is invisible,
// not when it is announced.
func warnOtherRunningWorkflows(app *App, g store.Group, current store.Workflow) {
	started, err := app.Store.StartedWorkflows(g.ID)
	if err != nil {
		return
	}

	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return
	}
	for _, w := range workflows {
		if w.ID == current.ID || !started[w.ID] {
			continue
		}
		fmt.Fprintln(app.Out, style.Row(style.Warn, g.Name+"/"+w.Name,
			fmt.Sprintf("still has services running — `prizm down %s %s` to stop them", g.Name, w.Name)))
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(docker): up starts attached services after applying env files"
```

---

### Task 3: `prizm down`

**Files:**
- Create: `internal/cli/down.go`
- Modify: `internal/cli/root.go` (register)
- Test: `internal/cli/down_test.go`

**Interfaces:**
- Produces: `prizm down [group] [workflow]` — stops one workflow's services, or every started workflow in the group when none is named. `--all` for the group-wide form when a workflow could otherwise be inferred.

`down` deliberately does **not** unlink env files. Symlinks are harmless when idle, removing them would break editors and tooling that expect a `.env` to exist, and the spec only ever framed `down` as the inverse of the Docker half of `up`.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/down_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownStopsAWorkflowsServices(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")
	r.calls = nil

	if err := h.run(t, "down", "XYZ", "local"); err != nil {
		t.Fatalf("down error = %v", err)
	}

	joined := strings.Join(r.calls[len(r.calls)-1], " ")
	if !strings.Contains(joined, "down") || !strings.Contains(joined, "prizm-xyz-local") {
		t.Errorf("command = %q, want a compose down for this workflow's project", joined)
	}
}

func TestDownClearsTheStartedRecord(t *testing.T) {
	h := newHarness(t)
	h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")
	h.run(t, "down", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	started, _ := h.app.Store.StartedWorkflows(g.ID)
	if len(started) != 0 {
		t.Errorf("started = %v, want empty after down", started)
	}
}

func TestDownLeavesEnvFilesAlone(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")
	h.run(t, "down", "XYZ", "local")

	if _, err := os.Lstat(filepath.Join(beDir, ".env")); err != nil {
		t.Errorf("down removed the env file: %v — it is the inverse of the Docker half only", err)
	}
}

func TestDownWithoutAWorkflowStopsEveryStartedOne(t *testing.T) {
	h := newHarness(t)
	composeFile := filepath.Join(t.TempDir(), "qa.yml")
	os.WriteFile(composeFile, []byte("services: {}\n"), 0o644)

	_, r := h.seedDocker(t)
	h.run(t, "add-workflow", "XYZ", "production", "--repos", "backend", "--tag", "prod")
	h.run(t, "docker-add", "XYZ", "production", "--compose", composeFile)
	h.run(t, "up", "XYZ", "local")
	h.run(t, "up", "XYZ", "production", "--yes")

	r.calls = nil
	if err := h.run(t, "down", "XYZ", "--all"); err != nil {
		t.Fatalf("down --all error = %v", err)
	}

	downs := 0
	for _, call := range r.calls {
		for _, arg := range call {
			if arg == "down" {
				downs++
			}
		}
	}
	if downs != 2 {
		t.Errorf("issued %d compose downs, want 2", downs)
	}
}

func TestDownOnAWorkflowWithNothingRunningIsQuiet(t *testing.T) {
	h := newHarness(t)
	h.seedDocker(t)

	if err := h.run(t, "down", "XYZ", "local"); err != nil {
		t.Fatalf("down error = %v", err)
	}
	if !strings.Contains(h.out.String(), "nothing") {
		t.Errorf("output = %q, want a clear nothing-to-do message", h.out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestDown`
Expected: FAIL — unknown command `down`.

- [ ] **Step 3: Implement it**

Create `internal/cli/down.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/dockerctl"
	"github.com/troglodytto/prizm/internal/store"
)

func newDownCmd(app *App) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "down [group] [workflow]",
		Short: "Stop a workflow's attached services",
		Long: "down is the inverse of up's Docker half only. Env symlinks are left in\n" +
			"place: they are harmless idle, and removing them breaks tooling that\n" +
			"expects a .env to exist.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, len(args))
			if err != nil {
				return err
			}

			workflows, err := app.Store.ListWorkflows(g.ID)
			if err != nil {
				return err
			}

			targets := workflows
			if len(rest) > 0 && !all {
				wf, err := app.Store.WorkflowByName(g.ID, rest[0])
				if err != nil {
					return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
				}
				targets = []store.Workflow{wf}
			}

			started, err := app.Store.StartedWorkflows(g.ID)
			if err != nil {
				return err
			}

			stopped := 0
			for _, wf := range targets {
				if !started[wf.ID] {
					continue
				}
				if err := stopServices(app, g, wf); err != nil {
					return err
				}
				stopped++
			}

			if stopped == 0 {
				fmt.Fprintf(app.Out, "nothing running in %s\n", g.Name)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "stop every started workflow in the group")
	return cmd
}

func stopServices(app *App, g store.Group, wf store.Workflow) error {
	attachment, ok, err := app.Store.AttachmentFor(wf.ID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	project := dockerctl.ProjectName(g.Name, wf.Name)
	if err := app.Compose.Down(project, attachment.ComposeFile); err != nil {
		return err
	}

	fmt.Fprintln(app.Out, style.Row(style.OK, g.Name+"/"+wf.Name, "services stopped"))
	return app.Store.MarkStopped(wf.ID)
}
```

Register it: `newDownCmd(app),`.

- [ ] **Step 4: Run the suite**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(docker): prizm down stops a workflow's services"
```

---

### Task 4: Containers in `status`, and live service progress

**Files:**
- Modify: `internal/cli/status.go`, `internal/cli/up.go`
- Test: `internal/cli/status_docker_test.go`

**Interfaces:**
- Produces: a `containers:` section in `prizm status`, and the attached services rendered as a `tui.Step` when `up --live` is used.

This closes the loop the spec drew: `status` shows what is running, which is what makes `down` the obvious next thing to reach for. It is also where the live progress from Phase 3 finally earns itself — container start-up is the one part of `up` that is genuinely slow enough to want a spinner.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/status_docker_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestStatusListsRunningContainers(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")

	r.out = `{"Name":"prizm-xyz-local-db-tunnel-1","Service":"db-tunnel","State":"running"}`
	h.out.Reset()

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "containers") {
		t.Errorf("output = %q, want a containers section", out)
	}
	if !strings.Contains(out, "db-tunnel") || !strings.Contains(out, "running") {
		t.Errorf("output = %q, want the service and its state", out)
	}
}

func TestStatusOmitsContainersWhenNoneAreAttached(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if strings.Contains(h.out.String(), "containers") {
		t.Errorf("output = %q, want no containers section", h.out.String())
	}
}

func TestStatusSurvivesADeadDaemon(t *testing.T) {
	h := newHarness(t)
	_, r := h.seedDocker(t)
	h.run(t, "up", "XYZ", "local")

	r.err = errDaemonDown
	h.out.Reset()

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Errorf("status error = %v, want nil — a dead daemon must not fail status", err)
	}
}
```

Declare the sentinel at the top of the file: `var errDaemonDown = errors.New("cannot connect to the Docker daemon")`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestStatusLists`
Expected: FAIL — no containers section.

- [ ] **Step 3: Add the section**

At the end of `newStatusCmd`'s `RunE`, before the drift hints:

```go
			if err := reportContainers(app, g); err != nil {
				return err
			}
```

and implement it in `internal/cli/status.go`:

```go
// reportContainers lists the services of every workflow prizm has started. A
// dead daemon reads as nothing running, never as a status failure.
func reportContainers(app *App, g store.Group) error {
	started, err := app.Store.StartedWorkflows(g.ID)
	if err != nil || len(started) == 0 {
		return err
	}

	attachments, err := app.Store.AttachmentsIn(g.ID)
	if err != nil {
		return err
	}
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return err
	}

	printed := false
	for _, wf := range workflows {
		if !started[wf.ID] {
			continue
		}
		attachment, ok := attachments[wf.ID]
		if !ok {
			continue
		}

		services, err := app.Compose.PS(dockerctl.ProjectName(g.Name, wf.Name), attachment.ComposeFile)
		if err != nil {
			return err
		}
		if len(services) == 0 {
			continue
		}

		if !printed {
			fmt.Fprintln(app.Out, "\n  containers:")
			printed = true
		}
		for _, s := range services {
			fmt.Fprintf(app.Out, "    %-16s %-10s (%s)\n", s.Name, s.State, wf.Name)
		}
	}

	if printed {
		fmt.Fprintln(app.Out, "\n"+style.Hint("run `prizm down` to stop them"))
	}
	return nil
}
```

- [ ] **Step 4: Add services to the live run**

In the `--live` branch of `applyWorkflow`, append one more step after the repo steps:

```go
		if attachment, ok, err := app.Store.AttachmentFor(wf.ID); err == nil && ok && !noDocker {
			steps = append(steps, tui.Step{
				Name: "services",
				Run: func() (string, error) {
					project := dockerctl.ProjectName(g.Name, wf.Name)
					if err := app.Compose.Up(project, attachment.ComposeFile, attachment.Services); err != nil {
						return "", err
					}
					if err := app.Store.MarkStarted(wf.ID, app.Now()); err != nil {
						return "", err
					}
					return "started", nil
				},
			})
		}
```

Services are last in the step list for the same reason they are last in the plain path: env files are the guarantee, containers are the convenience.

- [ ] **Step 5: Run the full suite and try it**

Run: `go test ./... && go build ./...`

```bash
go build -o /tmp/prizm .
/tmp/prizm docker-add DEMO local --compose ./local.yml --services db-tunnel
/tmp/prizm DEMO local --live
/tmp/prizm status DEMO
/tmp/prizm down DEMO local
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "feat(docker): container state in status and live service progress"
```

---

## Phase 5 Self-Review

**Spec coverage.** Compose scoped to the environment rather than the repo → Task 1. Shelling out to the CLI rather than the SDK, with the spec's own reasoning recorded → Task 1. Best-effort and clearly separated from env setting, failing loudly and separately → Task 2, with `TestUpAppliesEnvEvenWhenDockerFails` as the guard. A spinner for container start-up → Task 4. `prizm down` → Task 3. The spec's open question about switching workflows → answered in the design section, with the reasoning and the one-line path to the other policy.

**Placeholder scan.** Task 1 Step 6 describes its command tests rather than listing them, because all five are direct analogues of tests written in full earlier in this plan. Everything else carries complete code.

**Type consistency.** `dockerctl.Compose` is a value type holding a `Runner`, so `App.Compose` is injectable without a pointer. `ProjectName` is the single source of the project string, used identically by `up`, `down` and `status` — a divergence there would make `down` unable to stop what `up` started, which is why it has its own test.

**Watch during execution:**

- `PS` swallows runner errors on purpose. That is right for `status`, but do not copy the pattern into `Up` or `Down`, where a failure genuinely must surface.
- The `--live` step closure captures `attachment` and `wf` from the enclosing scope; the repo steps in Phase 3 already re-bind `repo := repo` for the same reason. Getting this wrong starts the wrong project.
- `warnOtherRunningWorkflows` depends on `docker_started` being cleaned up by `down`. If `MarkStopped` is skipped, every subsequent `up` warns about containers that are long gone.
