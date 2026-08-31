# prizm Core Implementation Plan (Phase 1)

**Goal:** Build the `prizm` walking skeleton — a Go CLI that stores groups, repos, workflows and layered env vars in an encrypted local SQLite DB, resolves them into a single env file per repo, symlinks it into place with `prizm <group> up <workflow>`, and offers directory-aware dynamic shell tab-completion.

**Architecture:** A single static Go binary. Cobra owns the command tree and the dynamic completion hooks. All persistent state lives in one SQLite file (cgo driver, real C SQLite) under `$XDG_DATA_HOME/prizm/`. Variable *values* are encrypted at rest with AES-256-GCM using a key held in the OS keychain; all metadata (group/repo/workflow names, paths) stays plaintext so completion queries stay fast. `up` resolves three variable layers into one map, renders it to a built file under the prizm data dir, and atomically points `<repo>/.env` at it via symlink. Pure logic (parsing, rendering, merging, ranking, arg rewriting) lives in dependency-free packages that are unit-tested exhaustively; the store and CLI are thin shells over them.

**Tech Stack:** Go 1.23 · `github.com/spf13/cobra` (commands + dynamic completion) · `github.com/mattn/go-sqlite3` (cgo SQLite — the fast one) · `github.com/zalando/go-keyring` (OS keychain) · `github.com/google/go-cmp` (test diffs) · stdlib `crypto/aes`+`crypto/cipher` and `testing`.

**Spec:** `prizm-design-brainstorm-transcript.md` (repo root) — the full design brainstorm this plan implements.

## Global Constraints

- Go module path is exactly `github.com/troglodytto/prizm`. Go directive: `go 1.23`.
- **cgo is enabled and expected.** Use `github.com/mattn/go-sqlite3` (the C SQLite bindings) — it is meaningfully faster than the pure-Go transpilation, and completion latency is the one hard performance requirement in this tool. Builds require a C toolchain; that is an accepted tradeoff.
- **Bubble Tea, Bubbles and Huh are out of scope for Phase 1.** Lip Gloss is not: it is a string-styling library, not a TUI framework, and every phase prints status lines that must look the same. All output in this phase is plain text on stdout/stderr, styled through `internal/style`.
- **All user-facing output goes through `internal/style`.** No command prints a raw `✓`, `✗` or `⚠`, and none invents its own column width. One glyph set, one palette, one alignment, from the first line Phase 1 prints to the last screen Phase 5 renders.
- Every file the tool writes that can contain secrets is mode `0600`; every directory it creates is `0700`.
- Variable **values** are always encrypted before they touch SQLite. Names — group, repo, workflow, shared-group, and variable *keys* — are stored plaintext and are queryable.
- Tab-completion must never make a network call and must not block on the OS keychain. Completion queries read plaintext metadata columns only.
- Repo paths are a stable, group-level contract. They are stored absolute and are never rewritten by any command in this phase. (A `prizm repair` command is explicitly deferred.)
- Follow TDD strictly: every task writes a failing test first, watches it fail, then implements. Commit at the end of every task.
- Every exported function referenced in a later task must be defined in an earlier task with the exact name and signature given in that task's **Interfaces** block.

---

## Domain Vocabulary (from the spec)

- **Group** — top-level namespace, e.g. `XYZ`. Owns repos and workflows.
- **Repo** — a name plus a fixed absolute filesystem path, e.g. `backend` → `/home/u/code/backend`. Belongs to exactly one group.
- **Workflow** — a named bundle: an explicit subset of the group's repos, plus an optional freeform `tag` (`prod`/`qa`/`local`) used for guardrails later. Replaces the "environment" concept entirely. Examples: `local`, `frontend-only`, `production`.
- **Shared group** ("bag") — a named bag of variables scoped to `(workflow, repo-subset)`. Solves the `DB_URL` duplication case: one value, many repos, edited in one place. Each bag is **file-backed**: it has a real `.env` file you edit directly, reconciled into the DB by `prizm shared sync`.
- **Variable precedence**, low → high: `repo-shared` → `workflow-shared-group` → `repo+workflow specific`. Most specific wins.
- **Template** — a stored value containing `${NAME}` references, expanded at resolve time rather than at write time.
- **Internal variable** — any variable whose key begins with `_PRIZM_`. Referenceable from any template, **never written to a repo's env file**. These are the plumbing: shared credentials and shared derived values that repos point their own public names at.

## Interpolation

Values are stored literally and expanded only when `up` builds a repo's env file. This is what makes derived shared values work: the shared group holds the *recipe*, not the result, so editing one input updates every consumer at once.

**The emitted file is opaque.** Whoever opens `backend/.env` sees `DB_URL=postgres://…` and nothing else — not the shared value it came from, not the credentials that shared value was itself derived from. The derivation chain lives entirely in prizm.

```
shared group 'db'  (workflow: local; repos: backend, auth, ai)
  _PRIZM_DB_USER = svc_app
  _PRIZM_DB_PASS = hunter2
  _PRIZM_DB_URL  = postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app

backend, workflow 'local'          auth, workflow 'local'
  DB_URL = ${_PRIZM_DB_URL}            DATABASE_URL = ${_PRIZM_DB_URL}

backend/.env                       auth/.env
  DB_URL=postgres://svc_app:…        DATABASE_URL=postgres://svc_app:…
```

Three things fall out of that shape, all of them wanted:

- One value, defined once. Rotating the password edits `_PRIZM_DB_PASS` in one place and every consumer changes.
- Derivation can nest — `_PRIZM_DB_URL` is itself built from two other internal vars — and the depth is invisible downstream.
- Each repo names the value whatever its own code expects. `backend` wants `DB_URL`, `auth` wants `DATABASE_URL`; the shared plumbing does not care.

Rules, all enforced by `internal/resolve/expand.go`:

1. **Merge first, expand second.** A template is expanded against the repo's *fully merged* map (all three layers). A reference therefore resolves to whatever won precedence for that repo — so `backend` pinning `_PRIZM_DB_USER=svc_backend` in its repo+workflow layer changes its own `DB_URL` while auth and ai keep the shared value. The stored template stays identical in all three; only the expansion differs. **Divergence checks (`sync`, later phase) must compare templates, not expansions**, or a legitimate override reads as drift.
2. **`${NAME}` only — never bare `$NAME`.** Env values are full of `$` (passwords, regexes, shell snippets); bare-`$VAR` expansion would silently corrupt them. A `$` not followed by `{` is literal.
3. **`$${NAME}` escapes** to a literal `${NAME}` for values genuinely containing that syntax.
4. **Chains resolve recursively**, to any depth: `DB_URL` → `_PRIZM_DB_URL` → `_PRIZM_DB_USER`.
5. **Cycles are a hard error** naming the loop: `cycle: A → B → A`.
6. **An unresolved reference is a hard error, scoped to that repo.** `up` aborts that repo, leaves its existing env file untouched, and continues with the others — so one typo can't quietly ship an empty database password, and can't block the repos that are fine.
7. **No process-environment fallback.** `${HOME}` does not resolve from the calling shell. Output must not depend on which terminal invoked it.
8. **`_PRIZM_`-prefixed keys are dropped at render time**, after expansion. Internal-ness lives in the key name, which means it cannot be lost: an override of `_PRIZM_DB_PASS` is still `_PRIZM_`-named, so a secret can never be published by an edit that forgot a flag. That structural guarantee is why this is a naming rule rather than a boolean column.

## Authoring Shared Bags by File

Setting shared variables one `--set KEY=VALUE` at a time is the UX the spec already rejected for `edit`. So every shared bag is backed by a file, and the file is the thing you edit.

```bash
prizm shared add XYZ local db --repos backend,auth,ai
#   creates the bag and materialises its file (0600):
#   ~/.local/share/prizm/shared/XYZ/local/db.env
#   pass --file ./infra/db.env to keep it somewhere you own instead

$EDITOR ~/.local/share/prizm/shared/XYZ/local/db.env
prizm shared edit XYZ local db      # same file, opened for you — convenience only

prizm shared sync                   # reconcile every bag whose file changed
prizm shared sync XYZ local db      # or just this one
```

The file is ordinary `.env` text with one optional prizm header:

```sh
# prizm:repos backend,auth,ai

_PRIZM_DB_USER = svc_app
_PRIZM_DB_PASS = hunter2
_PRIZM_DB_URL  = postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app
```

That header is what makes "define a common variable across multiple repos at once" a single edit: the file defines both the variables *and* their audience, so `sync` reconciles membership alongside values and there is no second command to remember. Omit the header and membership stays CLI-managed (`prizm shared repos XYZ local db --add ai`).

**Semantics — deliberately the same trust model as `prizm sync`:** the file is the source of truth, reconciliation is explicit and never automatic, the diff is key-level (added / removed / changed, never line-level), and nothing is written without confirmation. `up` performs only the cheap read-only check and prints `⚠ shared bag 'db' differs from its file — run prizm shared sync`, then carries on and finishes. Divergence never blocks an apply.

**Two things to be aware of, by construction:**

- The bag file is **plaintext on disk** while the DB copy is encrypted. That is the cost of "just edit a file." Default location is inside prizm's own `0600` data dir; if you point `--file` at something inside a repo, gitignore it.
- Sync is a **full replace**, not a merge: a key deleted from the file is deleted from the bag. This is what makes the file authoritative and the diff honest. The confirmation prompt shows removals explicitly.

## Phase 2 Constraint: `prizm sync` Semantics

`prizm sync` (repo file → prizm, the bottom-up direction) is **not** in this phase, but two of its rules are decided now because they constrain what Phase 1 must store.

**A repo's env file is a symlink to a generated file.** Editing `backend/.env` edits prizm's built output. Sync therefore reads that file and diffs it against `Emit(Expand(Merge(...)))` — the exact map `up` last produced — then attributes each change back to a layer:

- a changed key whose winning definition came from the repo's own layer → write it back there;
- a changed key whose winning definition came from a **shared bag** → this is the propagation case. Show which other repos consume it and ask before writing to the bag;
- a removed key → removal from the layer that defined it, confirmed.

**A `_PRIZM_`-prefixed key typed into a repo's env file is adopted repo-locally.** It lands in that repo's `(workflow, repo)` layer — never auto-injected into a shared bag, because one repo's file edit must not silently add a variable to three other repos. Sync offers the promotion explicitly in the same confirmation, where the user has the context:

```
$ prizm sync
backend/.env → XYZ/local/backend
  ~ PORT           8080 → 9090
  + _PRIZM_API_KEY   (internal, backend only)
      promote to shared bag 'db' so auth and ai see it too? [y/N]

  note: _PRIZM_API_KEY will no longer appear in backend/.env —
        internal values are never written to disk.
```

That last note matters: because internal values are never emitted, a `_PRIZM_` line the user typed will vanish from the file on the next `up`. Correct behaviour, confusing if unannounced.

**What this requires of Phase 1** (and is already satisfied): layers stay separately queryable rather than pre-merged, `ForRepo` returns templates rather than expansions, and `Emit` is a distinct step — so sync can reconstruct exactly what was written and reason about where each key came from.

## Command Grammar

Canonical form is verb-first; the spec's group-first form is sugar that gets rewritten before Cobra ever sees it:

```
prizm XYZ up local      →  prizm up XYZ local     (explicit verb)
prizm XYZ local         →  prizm up XYZ local     (implicit `up`)
prizm XYZ               →  prizm ls XYZ           (list workflows; becomes the TUI picker in a later phase)
```

The rewrite rule: if `args[0]` is not a registered command but *is* a known group name, then `args[1]` is either a registered verb (move it to the front) or a workflow name (insert `up`). Because verbs win over workflow names, **workflow names may not collide with command names** — validated at creation time.

This works because of one invariant the whole command tree obeys: **every group-scoped verb takes the group as its first positional argument** (`up <group> <workflow>`, `var <group> <repo> KEY=V`, `import <group> <repo> <file>`, `shared <group> ...`). Moving the group from the front to the second slot is therefore always a valid rewrite, with no per-command knowledge.

## File Structure

```
go.mod
main.go                          thin: builds deps, calls cli.Execute()
internal/config/paths.go         XDG-aware locations for the DB and built env files
internal/style/style.go          glyphs, palette, column widths — every line prizm prints
internal/envfile/parse.go        .env text  → map[string]string
internal/envfile/render.go       map[string]string → .env text (sorted, quoted)
internal/crypto/cipher.go        Cipher interface + AES-256-GCM impl + Plaintext test double
internal/crypto/keyring.go       load-or-create the 32-byte key in the OS keychain
internal/store/store.go          Open(), schema migration, PRAGMAs
internal/store/groups.go         group CRUD + frecency counters
internal/store/repos.go          repo CRUD (absolute paths, env-file name)
internal/store/workflows.go      workflow CRUD + repo membership + reserved-name check
internal/store/vars.go           the three variable layers + shared groups
internal/resolve/merge.go        pure layer merge (precedence)
internal/resolve/expand.go       ${VAR} interpolation, chains, cycle detection
internal/resolve/resolve.go      store-aware: assemble layers for (workflow, repo)
internal/rank/rank.go            directory-aware + frecency ordering for completion
internal/apply/link.go           write built file, back up strangers, atomic symlink
internal/sharedfile/file.go      materialise / parse a shared bag's .env file + its repos header
internal/sharedfile/sync.go      diff a bag's file against the DB, apply after confirmation
internal/cli/root.go             root command, dependency wiring, Execute()
internal/cli/rewrite.go          group-first → verb-first argument rewriting
internal/cli/group.go            init, ls
internal/cli/repo.go             add-repo
internal/cli/workflow.go         add-workflow
internal/cli/vars.go             var set, import
internal/cli/shared.go           shared add, shared edit, shared sync
internal/cli/up.go               up
internal/cli/completion.go       dynamic ValidArgsFunction wiring
```

Test files sit beside their subjects (`internal/envfile/parse_test.go`, etc.), Go convention.

---

### Task 1: Project scaffold and XDG paths

**Files:**
- Create: `go.mod`, `main.go`, `internal/config/paths.go`
- Test: `internal/config/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.DataDir() (string, error)` — `$XDG_DATA_HOME/prizm`, falling back to `$HOME/.local/share/prizm`.
  - `config.DBPath() (string, error)` — `<DataDir>/prizm.db`.
  - `config.BuiltPath(group, workflow, repo string) (string, error)` — `<DataDir>/built/<group>/<workflow>/<repo>.env`.
  - `config.EnsureDir(dir string) error` — `os.MkdirAll` with mode `0700`.

- [ ] **Step 1: Initialise the module and directory skeleton**

```bash
cd /path/to/prizm
git init
go mod init github.com/troglodytto/prizm
mkdir -p internal/config internal/envfile internal/crypto internal/store internal/resolve internal/rank internal/apply internal/cli
printf 'prizm\n/dist/\n' > .gitignore
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/paths_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirUsesXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm"; got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/tmp/home-test")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	if want := "/tmp/home-test/.local/share/prizm"; got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDBPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := DBPath()
	if err != nil {
		t.Fatalf("DBPath() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm/prizm.db"; got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
}

func TestBuiltPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := BuiltPath("XYZ", "local", "backend")
	if err != nil {
		t.Fatalf("BuiltPath() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm/built/XYZ/local/backend.env"; got != want {
		t.Errorf("BuiltPath() = %q, want %q", got, want)
	}
}

func TestEnsureDirCreates0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", perm)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: DataDir`, `undefined: DBPath`, `undefined: BuiltPath`, `undefined: EnsureDir`.

- [ ] **Step 4: Write minimal implementation**

Create `internal/config/paths.go`:

```go
// Package config resolves the on-disk locations prizm uses.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

const appName = "prizm"

// DataDir is the root directory for all prizm state.
func DataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot determine home directory: set HOME or XDG_DATA_HOME")
		}
	}
	return filepath.Join(home, ".local", "share", appName), nil
}

// DBPath is the SQLite database file.
func DBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prizm.db"), nil
}

// BuiltPath is the resolved env file prizm generates for one (group, workflow, repo).
func BuiltPath(group, workflow, repo string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "built", group, workflow, repo+".env"), nil
}

// EnsureDir creates dir and its parents with owner-only permissions.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS — all five tests.

- [ ] **Step 6: Add the entrypoint stub**

Create `main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	// Replaced in Task 13 by cli.Execute().
	fmt.Fprintln(os.Stderr, "prizm: not wired up yet")
	os.Exit(1)
}
```

Run: `go build ./...`
Expected: builds cleanly.

- [ ] **Step 7: Commit**

```bash
git add go.mod main.go .gitignore internal/config/
git commit -m "feat(config): XDG-aware path resolution and project scaffold"
```

---

### Task 2: envfile parse and render

**Files:**
- Create: `internal/envfile/parse.go`, `internal/envfile/render.go`
- Test: `internal/envfile/parse_test.go`, `internal/envfile/render_test.go`

**Interfaces:**
- Consumes: nothing (pure package, stdlib only).
- Produces:
  - `envfile.Parse(text string) (map[string]string, error)` — parses `.env` text. Accepts optional `export ` prefix, `#` comments, blank lines, single- and double-quoted values (double-quoted values interpret `\n`, `\r`, `\t`, `\\`, `\"`; single-quoted are literal). Returns an error naming the 1-based line number on a malformed line.
  - `envfile.Render(vars map[string]string) string` — deterministic output: keys sorted ascending, one `KEY=value` per line, trailing newline. Values are emitted bare when they match `^[A-Za-z0-9_@%+=:,./-]*$`, otherwise double-quoted with `\`, `"`, newline, carriage return and tab escaped. `?` is bare-safe (inert on a shell assignment's right-hand side, and query-string DSNs are everywhere); `&` and `;` are not.

Why per-key maps rather than raw text: the spec calls for key-level diffing (audit, sync, divergence warnings) so that editor key-reordering is not mistaken for a change. Every layer in prizm is a `map[string]string`; text is only an edge format.

- [ ] **Step 1: Write the failing parse test**

Create `internal/envfile/parse_test.go`:

```go
package envfile

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "simple pairs",
			in:   "A=1\nB=two\n",
			want: map[string]string{"A": "1", "B": "two"},
		},
		{
			name: "comments and blank lines ignored",
			in:   "# leading comment\n\nA=1\n   # indented comment\nB=2\n",
			want: map[string]string{"A": "1", "B": "2"},
		},
		{
			name: "export prefix stripped",
			in:   "export A=1\n",
			want: map[string]string{"A": "1"},
		},
		{
			name: "surrounding whitespace trimmed on key and bare value",
			in:   "  A  =  1  \n",
			want: map[string]string{"A": "1"},
		},
		{
			name: "double quotes interpret escapes",
			in:   `A="line1\nline2"` + "\n",
			want: map[string]string{"A": "line1\nline2"},
		},
		{
			name: "single quotes are literal",
			in:   `A='line1\nline2'` + "\n",
			want: map[string]string{"A": `line1\nline2`},
		},
		{
			name: "hash inside quotes is not a comment",
			in:   `A="v#1"` + "\n",
			want: map[string]string{"A": "v#1"},
		},
		{
			name: "equals sign inside value preserved",
			in:   "DSN=postgres://u:p@h/db?a=b\n",
			want: map[string]string{"DSN": "postgres://u:p@h/db?a=b"},
		},
		{
			name: "empty value allowed",
			in:   "A=\n",
			want: map[string]string{"A": ""},
		},
		{
			name: "later duplicate wins",
			in:   "A=1\nA=2\n",
			want: map[string]string{"A": "2"},
		},
		{
			name: "crlf line endings",
			in:   "A=1\r\nB=2\r\n",
			want: map[string]string{"A": "1", "B": "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantSub string
	}{
		{name: "missing equals", in: "A=1\nNOTAPAIR\n", wantSub: "line 2"},
		{name: "empty key", in: "=value\n", wantSub: "line 1"},
		{name: "unterminated quote", in: `A="oops` + "\n", wantSub: "line 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.in)
			if err == nil {
				t.Fatalf("Parse() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Parse() error = %q, want it to mention %q", err, tt.wantSub)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go get github.com/google/go-cmp@latest && go test ./internal/envfile/`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Implement Parse**

Create `internal/envfile/parse.go`:

```go
// Package envfile converts between .env text and key/value maps.
package envfile

import (
	"fmt"
	"strings"
)

// Parse reads .env-style text into a map. Later duplicate keys win.
func Parse(text string) (map[string]string, error) {
	out := make(map[string]string)

	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, rest, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: missing '=' in %q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}

		value, err := parseValue(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out[key] = value
	}

	return out, nil
}

func parseValue(v string) (string, error) {
	if v == "" {
		return "", nil
	}

	switch v[0] {
	case '"':
		body, ok := strings.CutSuffix(v[1:], `"`)
		if !ok || len(v) < 2 {
			return "", fmt.Errorf("unterminated double quote")
		}
		return unescape(body), nil
	case '\'':
		body, ok := strings.CutSuffix(v[1:], `'`)
		if !ok || len(v) < 2 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return body, nil
	}

	// Bare value: an unquoted '#' starts a trailing comment.
	if idx := strings.Index(v, " #"); idx >= 0 {
		v = v[:idx]
	}
	return strings.TrimSpace(v), nil
}

func unescape(s string) string {
	r := strings.NewReplacer(
		`\n`, "\n",
		`\r`, "\r",
		`\t`, "\t",
		`\"`, `"`,
		`\\`, `\`,
	)
	return r.Replace(s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/envfile/ -run TestParse -v`
Expected: PASS.

- [ ] **Step 5: Write the failing render test**

Create `internal/envfile/render_test.go`:

```go
package envfile

import "testing"

func TestRenderSortsKeys(t *testing.T) {
	got := Render(map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"})
	want := "ALPHA=2\nMID=3\nZED=1\n"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRenderQuoting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "bare simple", value: "hello", want: "K=hello\n"},
		{name: "bare url", value: "postgres://u:p@h:5432/db?sslmode=disable", want: "K=postgres://u:p@h:5432/db?sslmode=disable\n"},
		{name: "empty stays bare", value: "", want: "K=\n"},
		{name: "space forces quotes", value: "a b", want: "K=\"a b\"\n"},
		{name: "hash forces quotes", value: "a#b", want: "K=\"a#b\"\n"},
		{name: "newline escaped", value: "a\nb", want: `K="a\nb"` + "\n"},
		{name: "tab escaped", value: "a\tb", want: `K="a\tb"` + "\n"},
		{name: "quote escaped", value: `a"b`, want: `K="a\"b"` + "\n"},
		{name: "backslash escaped", value: `a\b`, want: `K="a\\b"` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(map[string]string{"K": tt.value}); got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderEmptyMap(t *testing.T) {
	if got := Render(map[string]string{}); got != "" {
		t.Errorf("Render(empty) = %q, want %q", got, "")
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	want := map[string]string{
		"SIMPLE": "value",
		"SPACED": "two words",
		"MULTI":  "line1\nline2",
		"QUOTED": `has "quotes"`,
		"EMPTY":  "",
		"DSN":    "postgres://u:p@h/db?a=b",
	}

	got, err := Parse(Render(want))
	if err != nil {
		t.Fatalf("Parse(Render(v)) error = %v", err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("round trip %s = %q, want %q", k, got[k], v)
		}
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/envfile/ -run TestRender`
Expected: FAIL — `undefined: Render`.

- [ ] **Step 7: Implement Render**

Create `internal/envfile/render.go`:

```go
package envfile

import (
	"regexp"
	"sort"
	"strings"
)

var bareSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./?-]*$`)

// Render writes vars as .env text with keys in ascending order.
func Render(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quote(vars[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func quote(v string) string {
	if bareSafe.MatchString(v) {
		return v
	}
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + r.Replace(v) + `"`
}
```

- [ ] **Step 8: Run the whole package**

Run: `go test ./internal/envfile/ -v`
Expected: PASS — all parse, render and round-trip tests.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/envfile/
git commit -m "feat(envfile): .env parsing and deterministic rendering"
```

---

### Task 3: Value encryption

**Files:**
- Create: `internal/crypto/cipher.go`, `internal/crypto/keyring.go`
- Test: `internal/crypto/cipher_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `crypto.Cipher` interface — `Encrypt(plaintext string) ([]byte, error)` and `Decrypt(blob []byte) (string, error)`.
  - `crypto.NewAESGCM(key []byte) (Cipher, error)` — requires a 32-byte key; output blob is `nonce || ciphertext`.
  - `crypto.Plaintext{}` — a `Cipher` that does no encryption. **Tests only**; never wired into `main`.
  - `crypto.LoadOrCreateKey() ([]byte, error)` — fetches the 32-byte key from the OS keychain (service `prizm`, user `db-key`), generating and storing one on first run.

Rationale from the spec: encrypt the *values*, not the whole DB, so metadata stays queryable and shell completion stays instant.

- [ ] **Step 1: Write the failing test**

Create `internal/crypto/cipher_test.go`:

```go
package crypto

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestAESGCMRoundTrip(t *testing.T) {
	c, err := NewAESGCM(testKey())
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}

	for _, want := range []string{"", "hello", "postgres://u:p@h/db", "multi\nline\tvalue"} {
		blob, err := c.Encrypt(want)
		if err != nil {
			t.Fatalf("Encrypt(%q) error = %v", want, err)
		}
		got, err := c.Decrypt(blob)
		if err != nil {
			t.Fatalf("Decrypt() error = %v", err)
		}
		if got != want {
			t.Errorf("round trip = %q, want %q", got, want)
		}
	}
}

func TestAESGCMCiphertextIsNotPlaintext(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	blob, err := c.Encrypt("SUPERSECRET")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(blob, []byte("SUPERSECRET")) {
		t.Error("ciphertext contains the plaintext")
	}
}

func TestAESGCMNonceIsRandom(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same plaintext produced identical blobs; nonce is not random")
	}
}

func TestAESGCMRejectsBadKeyLength(t *testing.T) {
	if _, err := NewAESGCM([]byte("short")); err == nil {
		t.Error("NewAESGCM(short key) error = nil, want error")
	}
}

func TestAESGCMRejectsTamperedBlob(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	blob, _ := c.Encrypt("value")
	blob[len(blob)-1] ^= 0xFF

	if _, err := c.Decrypt(blob); err == nil {
		t.Error("Decrypt(tampered) error = nil, want authentication failure")
	}
}

func TestAESGCMRejectsTruncatedBlob(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Error("Decrypt(truncated) error = nil, want error")
	}
}

func TestPlaintextCipherRoundTrip(t *testing.T) {
	var c Cipher = Plaintext{}

	blob, err := c.Encrypt("value")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if got != "value" {
		t.Errorf("round trip = %q, want %q", got, "value")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/crypto/`
Expected: FAIL — `undefined: NewAESGCM`, `undefined: Cipher`, `undefined: Plaintext`.

- [ ] **Step 3: Implement the cipher**

Create `internal/crypto/cipher.go`:

```go
// Package crypto encrypts variable values at rest.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the required key length in bytes (AES-256).
const KeySize = 32

// Cipher encrypts and decrypts single variable values.
type Cipher interface {
	Encrypt(plaintext string) ([]byte, error)
	Decrypt(blob []byte) (string, error)
}

type aesgcm struct {
	aead cipher.AEAD
}

// NewAESGCM returns a Cipher using AES-256-GCM. key must be KeySize bytes.
func NewAESGCM(key []byte) (Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &aesgcm{aead: aead}, nil
}

func (c *aesgcm) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (c *aesgcm) Decrypt(blob []byte) (string, error) {
	n := c.aead.NonceSize()
	if len(blob) < n {
		return "", errors.New("ciphertext too short")
	}
	out, err := c.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(out), nil
}

// Plaintext is a no-op Cipher for tests. Never use it in production wiring.
type Plaintext struct{}

func (Plaintext) Encrypt(plaintext string) ([]byte, error) { return []byte(plaintext), nil }
func (Plaintext) Decrypt(blob []byte) (string, error)      { return string(blob), nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/crypto/ -v`
Expected: PASS — all seven tests.

- [ ] **Step 5: Implement the keychain key provider**

Create `internal/crypto/keyring.go`:

```go
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "prizm"
	keyringUser    = "db-key"
)

// LoadOrCreateKey returns the 32-byte database key from the OS keychain,
// generating and storing a fresh one the first time prizm runs.
func LoadOrCreateKey() ([]byte, error) {
	encoded, err := keyring.Get(keyringService, keyringUser)
	switch {
	case err == nil:
		key, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			return nil, fmt.Errorf("stored prizm key is corrupt: %w", decErr)
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf("stored prizm key is %d bytes, want %d", len(key), KeySize)
		}
		return key, nil

	case errors.Is(err, keyring.ErrNotFound):
		key := make([]byte, KeySize)
		if _, rErr := rand.Read(key); rErr != nil {
			return nil, rErr
		}
		if sErr := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); sErr != nil {
			return nil, fmt.Errorf("storing prizm key in the OS keychain: %w", sErr)
		}
		return key, nil

	default:
		return nil, fmt.Errorf("reading prizm key from the OS keychain: %w", err)
	}
}
```

Note: `LoadOrCreateKey` is intentionally untested — it talks to a real OS keychain, which is not available in CI. Everything downstream depends on the `Cipher` interface, so tests inject `Plaintext{}` instead.

- [ ] **Step 6: Verify it builds**

Run: `go get github.com/zalando/go-keyring@latest && go build ./... && go test ./internal/crypto/`
Expected: build succeeds, tests PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/crypto/
git commit -m "feat(crypto): AES-256-GCM value encryption with keychain-held key"
```

---

### Task 4: SQLite store — open, schema, permissions

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `crypto.Cipher`, `crypto.Plaintext` (Task 3).
- Produces:
  - `store.Store` struct with unexported `db *sql.DB` and `cipher crypto.Cipher` fields.
  - `store.Open(path string, c crypto.Cipher) (*Store, error)` — opens/creates the DB, applies the schema, forces mode `0600`, enables foreign keys and WAL.
  - `store.(*Store).Close() error`
  - `store.ErrNotFound`, `store.ErrExists` — sentinel errors used by every later store method.
  - `store.newTestStore(t *testing.T) *Store` (in `store_test.go`) — the shared test helper every later store test uses.

Schema notes: `groups` is quoted everywhere because SQLite treats `GROUPS` as a window-frame keyword. All timestamps are Unix seconds (`INTEGER`). All variable values are `BLOB` (ciphertext). `ON DELETE CASCADE` everywhere so deleting a group later cannot orphan rows.

- [ ] **Step 1: Write the failing test**

Create `internal/store/store_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/crypto"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "prizm.db")
	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
}

func TestOpenSetsOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db mode = %04o, want 0600", perm)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s1, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	s1.Close()

	s2, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("second Open() error = %v (schema not idempotent?)", err)
	}
	s2.Close()
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	s := newTestStore(t)

	var on int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if on != 1 {
		t.Error("foreign_keys pragma is off; cascading deletes will not work")
	}
}

func TestSchemaCreatesAllTables(t *testing.T) {
	s := newTestStore(t)

	want := []string{
		"groups", "repos", "workflows", "workflow_repos",
		"repo_vars", "shared_groups", "shared_group_repos",
		"shared_group_vars", "workflow_repo_vars", "applied",
	}

	for _, table := range want {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/`
Expected: FAIL — `undefined: Open`, `undefined: Store`.

- [ ] **Step 3: Implement the store**

Create `internal/store/store.go`:

```go
// Package store is prizm's SQLite persistence layer. Variable values are
// encrypted; everything else is plaintext so completion queries stay fast.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/troglodytto/prizm/internal/crypto"
)

// Sentinel errors returned by store methods.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// Store is a handle on the prizm database.
type Store struct {
	db     *sql.DB
	cipher crypto.Cipher
}

const schema = `
CREATE TABLE IF NOT EXISTS "groups" (
	id           INTEGER PRIMARY KEY,
	name         TEXT    NOT NULL UNIQUE,
	use_count    INTEGER NOT NULL DEFAULT 0,
	last_used_at INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS repos (
	id         INTEGER PRIMARY KEY,
	group_id   INTEGER NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	path       TEXT    NOT NULL,
	env_file   TEXT    NOT NULL DEFAULT '.env',
	created_at INTEGER NOT NULL,
	UNIQUE(group_id, name)
);
CREATE INDEX IF NOT EXISTS idx_repos_group ON repos(group_id);

CREATE TABLE IF NOT EXISTS workflows (
	id         INTEGER PRIMARY KEY,
	group_id   INTEGER NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	tag        TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	UNIQUE(group_id, name)
);
CREATE INDEX IF NOT EXISTS idx_workflows_group ON workflows(group_id);

CREATE TABLE IF NOT EXISTS workflow_repos (
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	repo_id     INTEGER NOT NULL REFERENCES repos(id)     ON DELETE CASCADE,
	PRIMARY KEY (workflow_id, repo_id)
);
CREATE INDEX IF NOT EXISTS idx_workflow_repos_repo ON workflow_repos(repo_id);

-- Layer 1: repo-shared, applies in every workflow that touches this repo.
CREATE TABLE IF NOT EXISTS repo_vars (
	repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	key        TEXT    NOT NULL,
	value      BLOB    NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (repo_id, key)
);

-- Layer 2: a named bag of vars scoped to (workflow, repo subset).
CREATE TABLE IF NOT EXISTS shared_groups (
	id          INTEGER PRIMARY KEY,
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	name        TEXT    NOT NULL,
	file_path   TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	UNIQUE(workflow_id, name)
);

CREATE TABLE IF NOT EXISTS shared_group_repos (
	shared_group_id INTEGER NOT NULL REFERENCES shared_groups(id) ON DELETE CASCADE,
	repo_id         INTEGER NOT NULL REFERENCES repos(id)         ON DELETE CASCADE,
	PRIMARY KEY (shared_group_id, repo_id)
);
CREATE INDEX IF NOT EXISTS idx_sgr_repo ON shared_group_repos(repo_id);

CREATE TABLE IF NOT EXISTS shared_group_vars (
	shared_group_id INTEGER NOT NULL REFERENCES shared_groups(id) ON DELETE CASCADE,
	key             TEXT    NOT NULL,
	value           BLOB    NOT NULL,
	updated_at      INTEGER NOT NULL,
	PRIMARY KEY (shared_group_id, key)
);

-- Layer 3: this repo, in this workflow. Highest precedence.
CREATE TABLE IF NOT EXISTS workflow_repo_vars (
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	repo_id     INTEGER NOT NULL REFERENCES repos(id)     ON DELETE CASCADE,
	key         TEXT    NOT NULL,
	value       BLOB    NOT NULL,
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (workflow_id, repo_id, key)
);

-- What is currently linked where (powers ` + "`prizm status`" + ` in a later phase).
CREATE TABLE IF NOT EXISTS applied (
	repo_id     INTEGER PRIMARY KEY REFERENCES repos(id)     ON DELETE CASCADE,
	workflow_id INTEGER NOT NULL    REFERENCES workflows(id) ON DELETE CASCADE,
	built_path  TEXT    NOT NULL,
	applied_at  INTEGER NOT NULL
);
`

// Open opens (creating if needed) the prizm database at path.
func Open(path string, c crypto.Cipher) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL",
		path,
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// One connection: this is a single-user local CLI, and it removes any
	// chance of a per-connection pragma being missed.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("securing database file: %w", err)
	}

	return &Store{db: db, cipher: c}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go get github.com/mattn/go-sqlite3@latest && go test ./internal/store/ -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Verify the cgo build and race-freedom**

Run: `CGO_ENABLED=1 go build ./... && go test -race ./internal/store/`
Expected: both succeed. A C toolchain (`gcc`/`clang`) must be installed; if the build fails with `cgo: C compiler "gcc" not found`, install build-essential.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/store/
git commit -m "feat(store): SQLite schema, WAL, foreign keys, 0600 perms"
```

---

### Task 5: Store — groups

**Files:**
- Create: `internal/store/groups.go`
- Test: `internal/store/groups_test.go`

**Interfaces:**
- Consumes: `store.Store`, `store.ErrExists`, `store.ErrNotFound`, `newTestStore` (Task 4).
- Produces:
  - `store.Group` struct: `ID int64`, `Name string`, `UseCount int`, `LastUsedAt time.Time`.
  - `store.(*Store).CreateGroup(name string) (Group, error)` — `ErrExists` on duplicate.
  - `store.(*Store).GroupByName(name string) (Group, error)` — `ErrNotFound` if absent.
  - `store.(*Store).ListGroups() ([]Group, error)` — ordered by name for stability; the *display* order is decided by `rank` (Task 10), not here.
  - `store.(*Store).TouchGroup(id int64, now time.Time) error` — increments `use_count`, sets `last_used_at`. Called by `up` to feed frecency ranking.

- [ ] **Step 1: Write the failing test**

Create `internal/store/groups_test.go`:

```go
package store

import (
	"errors"
	"testing"
	"time"
)

func TestCreateAndGetGroup(t *testing.T) {
	s := newTestStore(t)

	created, err := s.CreateGroup("XYZ")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if created.ID == 0 {
		t.Error("CreateGroup() returned zero ID")
	}
	if created.Name != "XYZ" {
		t.Errorf("Name = %q, want %q", created.Name, "XYZ")
	}

	got, err := s.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName() error = %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GroupByName().ID = %d, want %d", got.ID, created.ID)
	}
}

func TestCreateGroupRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateGroup("XYZ"); err != nil {
		t.Fatalf("first CreateGroup() error = %v", err)
	}

	_, err := s.CreateGroup("XYZ")
	if !errors.Is(err, ErrExists) {
		t.Errorf("second CreateGroup() error = %v, want ErrExists", err)
	}
}

func TestGroupByNameMissing(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GroupByName("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GroupByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListGroupsSortedByName(t *testing.T) {
	s := newTestStore(t)

	for _, n := range []string{"zeta", "alpha", "mid"} {
		if _, err := s.CreateGroup(n); err != nil {
			t.Fatalf("CreateGroup(%q) error = %v", n, err)
		}
	}

	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}

	want := []string{"alpha", "mid", "zeta"}
	if len(groups) != len(want) {
		t.Fatalf("ListGroups() returned %d groups, want %d", len(groups), len(want))
	}
	for i, w := range want {
		if groups[i].Name != w {
			t.Errorf("groups[%d].Name = %q, want %q", i, groups[i].Name, w)
		}
	}
}

func TestTouchGroupUpdatesFrecency(t *testing.T) {
	s := newTestStore(t)

	g, _ := s.CreateGroup("XYZ")
	now := time.Unix(1700000000, 0)

	if err := s.TouchGroup(g.ID, now); err != nil {
		t.Fatalf("TouchGroup() error = %v", err)
	}
	if err := s.TouchGroup(g.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("second TouchGroup() error = %v", err)
	}

	got, err := s.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName() error = %v", err)
	}
	if got.UseCount != 2 {
		t.Errorf("UseCount = %d, want 2", got.UseCount)
	}
	if want := now.Add(time.Hour).Unix(); got.LastUsedAt.Unix() != want {
		t.Errorf("LastUsedAt = %d, want %d", got.LastUsedAt.Unix(), want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestGroup`
Expected: FAIL — `s.CreateGroup undefined`.

- [ ] **Step 3: Implement groups**

Create `internal/store/groups.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Group is a top-level namespace owning repos and workflows.
type Group struct {
	ID         int64
	Name       string
	UseCount   int
	LastUsedAt time.Time
}

// CreateGroup registers a new group.
func (s *Store) CreateGroup(name string) (Group, error) {
	res, err := s.db.Exec(
		`INSERT INTO "groups"(name, created_at) VALUES (?, ?)`,
		name, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Group{}, fmt.Errorf("group %q: %w", name, ErrExists)
		}
		return Group{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Group{}, err
	}
	return Group{ID: id, Name: name}, nil
}

// GroupByName looks a group up by its exact name.
func (s *Store) GroupByName(name string) (Group, error) {
	var (
		g       Group
		lastUsed int64
	)
	err := s.db.QueryRow(
		`SELECT id, name, use_count, last_used_at FROM "groups" WHERE name = ?`, name,
	).Scan(&g.ID, &g.Name, &g.UseCount, &lastUsed)

	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, fmt.Errorf("group %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Group{}, err
	}
	g.LastUsedAt = time.Unix(lastUsed, 0)
	return g, nil
}

// ListGroups returns every group ordered by name.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(
		`SELECT id, name, use_count, last_used_at FROM "groups" ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		var (
			g        Group
			lastUsed int64
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.UseCount, &lastUsed); err != nil {
			return nil, err
		}
		g.LastUsedAt = time.Unix(lastUsed, 0)
		out = append(out, g)
	}
	return out, rows.Err()
}

// TouchGroup records a use of the group, feeding frecency ranking.
func (s *Store) TouchGroup(id int64, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE "groups" SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`,
		now.Unix(), id,
	)
	return err
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// Matching on message text keeps this driver-agnostic.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS — group tests plus the Task 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): group CRUD with frecency counters"
```

---

### Task 6: Store — repos

**Files:**
- Create: `internal/store/repos.go`
- Test: `internal/store/repos_test.go`

**Interfaces:**
- Consumes: Task 5's `Group`, `CreateGroup`.
- Produces:
  - `store.Repo` struct: `ID int64`, `GroupID int64`, `Name string`, `Path string`, `EnvFile string`.
  - `store.(*Store).AddRepo(groupID int64, name, path, envFile string) (Repo, error)` — `path` is stored as given (the CLI resolves it to absolute first); empty `envFile` defaults to `.env`; `ErrExists` on duplicate `(group, name)`.
  - `store.(*Store).RepoByName(groupID int64, name string) (Repo, error)`
  - `store.(*Store).ListRepos(groupID int64) ([]Repo, error)` — ordered by name.
  - `store.(*Store).RepoPathsByGroup() (map[string][]string, error)` — group name → repo paths. Feeds the directory-aware completion ranking without an N+1 query.

- [ ] **Step 1: Write the failing test**

Create `internal/store/repos_test.go`:

```go
package store

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddAndGetRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	added, err := s.AddRepo(g.ID, "backend", "/home/u/code/backend", "")
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if added.EnvFile != ".env" {
		t.Errorf("EnvFile = %q, want %q (default)", added.EnvFile, ".env")
	}

	got, err := s.RepoByName(g.ID, "backend")
	if err != nil {
		t.Fatalf("RepoByName() error = %v", err)
	}
	if got.Path != "/home/u/code/backend" {
		t.Errorf("Path = %q, want %q", got.Path, "/home/u/code/backend")
	}
	if got.GroupID != g.ID {
		t.Errorf("GroupID = %d, want %d", got.GroupID, g.ID)
	}
}

func TestAddRepoCustomEnvFile(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	added, err := s.AddRepo(g.ID, "frontend", "/home/u/code/frontend", ".env.local")
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if added.EnvFile != ".env.local" {
		t.Errorf("EnvFile = %q, want %q", added.EnvFile, ".env.local")
	}
}

func TestAddRepoRejectsDuplicateNameInSameGroup(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddRepo(g.ID, "backend", "/a", ""); err != nil {
		t.Fatalf("first AddRepo() error = %v", err)
	}

	_, err := s.AddRepo(g.ID, "backend", "/b", "")
	if !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddRepo() error = %v, want ErrExists", err)
	}
}

func TestAddRepoAllowsSameNameInDifferentGroups(t *testing.T) {
	s := newTestStore(t)
	g1, _ := s.CreateGroup("XYZ")
	g2, _ := s.CreateGroup("ABC")

	if _, err := s.AddRepo(g1.ID, "backend", "/a", ""); err != nil {
		t.Fatalf("AddRepo(g1) error = %v", err)
	}
	if _, err := s.AddRepo(g2.ID, "backend", "/b", ""); err != nil {
		t.Errorf("AddRepo(g2) error = %v, want nil", err)
	}
}

func TestRepoByNameMissing(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	_, err := s.RepoByName(g.ID, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListReposSortedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	for _, n := range []string{"frontend", "ai", "backend"} {
		s.AddRepo(g.ID, n, "/"+n, "")
	}

	repos, err := s.ListRepos(g.ID)
	if err != nil {
		t.Fatalf("ListRepos() error = %v", err)
	}

	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if diff := cmp.Diff([]string{"ai", "backend", "frontend"}, names); diff != "" {
		t.Errorf("ListRepos() names mismatch (-want +got):\n%s", diff)
	}
}

func TestRepoPathsByGroup(t *testing.T) {
	s := newTestStore(t)
	g1, _ := s.CreateGroup("XYZ")
	g2, _ := s.CreateGroup("ABC")
	s.AddRepo(g1.ID, "backend", "/code/backend", "")
	s.AddRepo(g1.ID, "frontend", "/code/frontend", "")
	s.AddRepo(g2.ID, "svc", "/other/svc", "")

	got, err := s.RepoPathsByGroup()
	if err != nil {
		t.Fatalf("RepoPathsByGroup() error = %v", err)
	}

	want := map[string][]string{
		"XYZ": {"/code/backend", "/code/frontend"},
		"ABC": {"/other/svc"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RepoPathsByGroup() mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRepo`
Expected: FAIL — `s.AddRepo undefined`.

- [ ] **Step 3: Implement repos**

Create `internal/store/repos.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Repo is a named checkout at a fixed absolute path.
type Repo struct {
	ID      int64
	GroupID int64
	Name    string
	Path    string
	EnvFile string
}

// DefaultEnvFile is the file prizm symlinks inside a repo when none is given.
const DefaultEnvFile = ".env"

// AddRepo registers a repo in a group. path must already be absolute.
func (s *Store) AddRepo(groupID int64, name, path, envFile string) (Repo, error) {
	if envFile == "" {
		envFile = DefaultEnvFile
	}

	res, err := s.db.Exec(
		`INSERT INTO repos(group_id, name, path, env_file, created_at) VALUES (?, ?, ?, ?, ?)`,
		groupID, name, path, envFile, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Repo{}, fmt.Errorf("repo %q: %w", name, ErrExists)
		}
		return Repo{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Repo{}, err
	}
	return Repo{ID: id, GroupID: groupID, Name: name, Path: path, EnvFile: envFile}, nil
}

// RepoByName looks a repo up within a group.
func (s *Store) RepoByName(groupID int64, name string) (Repo, error) {
	var r Repo
	err := s.db.QueryRow(
		`SELECT id, group_id, name, path, env_file FROM repos WHERE group_id = ? AND name = ?`,
		groupID, name,
	).Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile)

	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Repo{}, err
	}
	return r, nil
}

// ListRepos returns a group's repos ordered by name.
func (s *Store) ListRepos(groupID int64) ([]Repo, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, name, path, env_file FROM repos WHERE group_id = ? ORDER BY name`,
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RepoPathsByGroup maps every group name to its repo paths in one query.
// Used by completion, which must stay fast.
func (s *Store) RepoPathsByGroup() (map[string][]string, error) {
	rows, err := s.db.Query(`
		SELECT g.name, r.path
		FROM "groups" g
		JOIN repos r ON r.group_id = g.id
		ORDER BY g.name, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string)
	for rows.Next() {
		var group, path string
		if err := rows.Scan(&group, &path); err != nil {
			return nil, err
		}
		out[group] = append(out[group], path)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): repo registration with fixed paths and env-file names"
```

---

### Task 7: Store — workflows and repo membership

**Files:**
- Create: `internal/store/workflows.go`
- Test: `internal/store/workflows_test.go`

**Interfaces:**
- Consumes: Task 5 `CreateGroup`, Task 6 `AddRepo`/`Repo`.
- Produces:
  - `store.Workflow` struct: `ID int64`, `GroupID int64`, `Name string`, `Tag string`.
  - `store.ErrReservedName` — sentinel.
  - `store.IsReservedName(name string) bool` — true for anything that is (or will be) a prizm verb.
  - `store.(*Store).AddWorkflow(groupID int64, name, tag string, repoIDs []int64) (Workflow, error)` — creates the workflow and its membership rows in one transaction.
  - `store.(*Store).WorkflowByName(groupID int64, name string) (Workflow, error)`
  - `store.(*Store).ListWorkflows(groupID int64) ([]Workflow, error)` — ordered by name.
  - `store.(*Store).WorkflowRepos(workflowID int64) ([]Repo, error)` — ordered by repo name.

Why reserved names: the argument rewriting in Task 13 resolves `prizm XYZ <word>` by asking "is `<word>` a verb?" first. A workflow named `status` would become permanently unreachable in the sugar form. Rejecting the name at creation is the cheap fix. The list deliberately includes verbs not yet built (`sync`, `edit`, `audit`, `docker`, `down`, `status`, `repair`) so a later phase can't be blocked by data created today.

- [ ] **Step 1: Write the failing test**

Create `internal/store/workflows_test.go`:

```go
package store

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddWorkflowWithRepos(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	s.AddRepo(g.ID, "ai", "/code/ai", "")

	wf, err := s.AddWorkflow(g.ID, "local", "local", []int64{fe.ID, be.ID})
	if err != nil {
		t.Fatalf("AddWorkflow() error = %v", err)
	}
	if wf.Tag != "local" {
		t.Errorf("Tag = %q, want %q", wf.Tag, "local")
	}

	repos, err := s.WorkflowRepos(wf.ID)
	if err != nil {
		t.Fatalf("WorkflowRepos() error = %v", err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if diff := cmp.Diff([]string{"backend", "frontend"}, names); diff != "" {
		t.Errorf("WorkflowRepos() mismatch (-want +got):\n%s", diff)
	}
}

func TestAddWorkflowWithNoReposIsAllowed(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	wf, err := s.AddWorkflow(g.ID, "empty", "", nil)
	if err != nil {
		t.Fatalf("AddWorkflow() error = %v", err)
	}

	repos, err := s.WorkflowRepos(wf.ID)
	if err != nil {
		t.Fatalf("WorkflowRepos() error = %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("WorkflowRepos() = %d repos, want 0", len(repos))
	}
}

func TestAddWorkflowRejectsReservedNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	for _, name := range []string{"up", "status", "sync", "edit", "import", "ls", "help"} {
		_, err := s.AddWorkflow(g.ID, name, "", nil)
		if !errors.Is(err, ErrReservedName) {
			t.Errorf("AddWorkflow(%q) error = %v, want ErrReservedName", name, err)
		}
	}
}

func TestAddWorkflowRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddWorkflow(g.ID, "local", "", nil); err != nil {
		t.Fatalf("first AddWorkflow() error = %v", err)
	}

	_, err := s.AddWorkflow(g.ID, "local", "", nil)
	if !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddWorkflow() error = %v, want ErrExists", err)
	}
}

func TestAddWorkflowRollsBackOnBadRepoID(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddWorkflow(g.ID, "local", "", []int64{9999}); err == nil {
		t.Fatal("AddWorkflow(bad repo id) error = nil, want foreign key error")
	}

	if _, err := s.WorkflowByName(g.ID, "local"); !errors.Is(err, ErrNotFound) {
		t.Errorf("workflow survived a failed AddWorkflow; transaction did not roll back")
	}
}

func TestWorkflowByNameMissing(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	_, err := s.WorkflowByName(g.ID, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("WorkflowByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListWorkflowsSortedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	for _, n := range []string{"production", "debug-payments", "local"} {
		s.AddWorkflow(g.ID, n, "", nil)
	}

	wfs, err := s.ListWorkflows(g.ID)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	var names []string
	for _, w := range wfs {
		names = append(names, w.Name)
	}
	if diff := cmp.Diff([]string{"debug-payments", "local", "production"}, names); diff != "" {
		t.Errorf("ListWorkflows() mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWorkflow`
Expected: FAIL — `s.AddWorkflow undefined`.

- [ ] **Step 3: Implement workflows**

Create `internal/store/workflows.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrReservedName is returned when a workflow name collides with a prizm verb.
var ErrReservedName = errors.New("name is reserved")

// reservedNames are the words that can appear where a verb is expected in
// `prizm <group> <word>`. Includes verbs planned for later phases so data
// created today cannot block them.
var reservedNames = map[string]bool{
	"up": true, "down": true, "ls": true, "list": true, "status": true,
	"init": true, "add-repo": true, "add-workflow": true, "var": true,
	"import": true, "edit": true, "sync": true, "audit": true,
	"docker": true, "repair": true, "completion": true, "help": true,
	"version": true,
}

// IsReservedName reports whether name may not be used as a workflow name.
func IsReservedName(name string) bool { return reservedNames[name] }

// Workflow is a named bundle of repos plus an optional guardrail tag.
type Workflow struct {
	ID      int64
	GroupID int64
	Name    string
	Tag     string
}

// AddWorkflow creates a workflow and its repo membership atomically.
func (s *Store) AddWorkflow(groupID int64, name, tag string, repoIDs []int64) (Workflow, error) {
	if IsReservedName(name) {
		return Workflow{}, fmt.Errorf("workflow %q: %w (it is a prizm command)", name, ErrReservedName)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Workflow{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO workflows(group_id, name, tag, created_at) VALUES (?, ?, ?, ?)`,
		groupID, name, tag, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Workflow{}, fmt.Errorf("workflow %q: %w", name, ErrExists)
		}
		return Workflow{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Workflow{}, err
	}

	for _, repoID := range repoIDs {
		if _, err := tx.Exec(
			`INSERT INTO workflow_repos(workflow_id, repo_id) VALUES (?, ?)`, id, repoID,
		); err != nil {
			return Workflow{}, fmt.Errorf("adding repo %d to workflow %q: %w", repoID, name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Workflow{}, err
	}
	return Workflow{ID: id, GroupID: groupID, Name: name, Tag: tag}, nil
}

// WorkflowByName looks a workflow up within a group.
func (s *Store) WorkflowByName(groupID int64, name string) (Workflow, error) {
	var w Workflow
	err := s.db.QueryRow(
		`SELECT id, group_id, name, tag FROM workflows WHERE group_id = ? AND name = ?`,
		groupID, name,
	).Scan(&w.ID, &w.GroupID, &w.Name, &w.Tag)

	if errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, fmt.Errorf("workflow %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// ListWorkflows returns a group's workflows ordered by name.
func (s *Store) ListWorkflows(groupID int64) ([]Workflow, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, name, tag FROM workflows WHERE group_id = ? ORDER BY name`,
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		var w Workflow
		if err := rows.Scan(&w.ID, &w.GroupID, &w.Name, &w.Tag); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkflowRepos returns the repos this workflow touches, ordered by name.
func (s *Store) WorkflowRepos(workflowID int64) ([]Repo, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file
		FROM repos r
		JOIN workflow_repos wr ON wr.repo_id = r.id
		WHERE wr.workflow_id = ?
		ORDER BY r.name`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): workflows, explicit repo membership, reserved names"
```


---

### Task 8: Store — the three variable layers and shared groups

**Files:**
- Create: `internal/store/vars.go`
- Test: `internal/store/vars_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–7, plus `crypto.NewAESGCM` (Task 3) for the encryption-at-rest test.
- Produces:
  - `store.ErrInvalidKey` — sentinel; keys must match `^[A-Za-z_][A-Za-z0-9_]*$` (which admits `_PRIZM_`-prefixed internal keys without a special case).
  - `store.SharedGroup` struct: `ID int64`, `WorkflowID int64`, `Name string`.
  - Layer 1: `SetRepoVar(repoID int64, key, value string) error`, `RepoVars(repoID int64) (map[string]string, error)`
  - Layer 2: `CreateSharedGroup(workflowID int64, name string) (SharedGroup, error)`, `SharedGroupByName(workflowID int64, name string) (SharedGroup, error)`, `AddSharedGroupRepo(sharedGroupID, repoID int64) error`, `SetSharedGroupVar(sharedGroupID int64, key, value string) error`, `SharedGroupVars(sharedGroupID int64) (map[string]string, error)`, `SharedGroupsForRepo(workflowID, repoID int64) ([]SharedGroup, error)` (ordered by name)
  - Layer 3: `SetWorkflowRepoVar(workflowID, repoID int64, key, value string) error`, `WorkflowRepoVars(workflowID, repoID int64) (map[string]string, error)`

Every layer is a plain `map[string]string` of **stored templates** — the store never expands anything. `Set*` methods are upserts; getters decrypt. `SharedGroupsForRepo` orders by name so that when a repo belongs to two shared groups that both define the same key, the winner is deterministic (later name wins) rather than dependent on insertion order — documented behaviour, not an accident.

- [ ] **Step 1: Write the failing test**

Create `internal/store/vars_test.go`:

```go
package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/troglodytto/prizm/internal/crypto"
)

func TestRepoVarsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	if err := s.SetRepoVar(r.ID, "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("SetRepoVar() error = %v", err)
	}

	got, err := s.RepoVars(r.ID)
	if err != nil {
		t.Fatalf("RepoVars() error = %v", err)
	}
	if diff := cmp.Diff(map[string]string{"LOG_LEVEL": "debug"}, got); diff != "" {
		t.Errorf("RepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestSetRepoVarUpserts(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	s.SetRepoVar(r.ID, "K", "first")
	if err := s.SetRepoVar(r.ID, "K", "second"); err != nil {
		t.Fatalf("second SetRepoVar() error = %v", err)
	}

	got, _ := s.RepoVars(r.ID)
	if got["K"] != "second" {
		t.Errorf("K = %q, want %q", got["K"], "second")
	}
}

func TestSetVarRejectsInvalidKeys(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	for _, key := range []string{"", "1LEADING_DIGIT", "has-dash", "has space", "has=equals"} {
		if err := s.SetRepoVar(r.ID, key, "v"); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("SetRepoVar(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}

func TestSetVarAcceptsInternalKeys(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	if err := s.SetRepoVar(r.ID, "_PRIZM_DB_PASS", "hunter2"); err != nil {
		t.Errorf("SetRepoVar(_PRIZM_DB_PASS) error = %v, want nil", err)
	}
}

// The store is a dumb template holder: it must never expand anything.
func TestStoreKeepsTemplatesLiteral(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	tmpl := "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@h/db"
	s.SetRepoVar(r.ID, "_PRIZM_DB_URL", tmpl)

	got, _ := s.RepoVars(r.ID)
	if got["_PRIZM_DB_URL"] != tmpl {
		t.Errorf("_PRIZM_DB_URL = %q, want the literal template %q", got["_PRIZM_DB_URL"], tmpl)
	}
}

func TestWorkflowRepoVarsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	if err := s.SetWorkflowRepoVar(wf.ID, r.ID, "PORT", "3000"); err != nil {
		t.Fatalf("SetWorkflowRepoVar() error = %v", err)
	}

	got, err := s.WorkflowRepoVars(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("WorkflowRepoVars() error = %v", err)
	}
	if diff := cmp.Diff(map[string]string{"PORT": "3000"}, got); diff != "" {
		t.Errorf("WorkflowRepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestWorkflowRepoVarsAreScopedPerWorkflow(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	local, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	prod, _ := s.AddWorkflow(g.ID, "production", "prod", []int64{r.ID})

	s.SetWorkflowRepoVar(local.ID, r.ID, "API", "http://localhost:8080")
	s.SetWorkflowRepoVar(prod.ID, r.ID, "API", "https://api.example.com")

	gotLocal, _ := s.WorkflowRepoVars(local.ID, r.ID)
	gotProd, _ := s.WorkflowRepoVars(prod.ID, r.ID)

	if gotLocal["API"] != "http://localhost:8080" {
		t.Errorf("local API = %q", gotLocal["API"])
	}
	if gotProd["API"] != "https://api.example.com" {
		t.Errorf("prod API = %q", gotProd["API"])
	}
}

func TestSharedGroupMembershipAndVars(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID, fe.ID})

	sg, err := s.CreateSharedGroup(wf.ID, "db")
	if err != nil {
		t.Fatalf("CreateSharedGroup() error = %v", err)
	}
	if err := s.AddSharedGroupRepo(sg.ID, be.ID); err != nil {
		t.Fatalf("AddSharedGroupRepo() error = %v", err)
	}
	s.AddSharedGroupRepo(sg.ID, auth.ID)

	// The derived-credentials shape from the spec: internal plumbing only.
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	vars, err := s.SharedGroupVars(sg.ID)
	if err != nil {
		t.Fatalf("SharedGroupVars() error = %v", err)
	}
	if len(vars) != 3 {
		t.Errorf("SharedGroupVars() = %d entries, want 3", len(vars))
	}

	memberOf, err := s.SharedGroupsForRepo(wf.ID, be.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo() error = %v", err)
	}
	if len(memberOf) != 1 || memberOf[0].Name != "db" {
		t.Errorf("backend shared groups = %+v, want one named db", memberOf)
	}

	// frontend was never added to the shared group, so it must see none.
	none, err := s.SharedGroupsForRepo(wf.ID, fe.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo(frontend) error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("frontend shared groups = %+v, want none", none)
	}
}

func TestSharedGroupsForRepoOrderedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	for _, name := range []string{"zzz", "aaa", "mmm"} {
		sg, _ := s.CreateSharedGroup(wf.ID, name)
		s.AddSharedGroupRepo(sg.ID, r.ID)
	}

	got, err := s.SharedGroupsForRepo(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo() error = %v", err)
	}
	var names []string
	for _, sg := range got {
		names = append(names, sg.Name)
	}
	if diff := cmp.Diff([]string{"aaa", "mmm", "zzz"}, names); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
}

func TestCreateSharedGroupRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)

	s.CreateSharedGroup(wf.ID, "db")
	if _, err := s.CreateSharedGroup(wf.ID, "db"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate CreateSharedGroup() error = %v, want ErrExists", err)
	}
}

// The whole point of the crypto layer: a leaked DB file must not leak secrets.
func TestValuesAreEncryptedAtRest(t *testing.T) {
	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := crypto.NewAESGCM(key)
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "prizm.db")
	s, err := Open(path, c)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	if err := s.SetRepoVar(r.ID, "_PRIZM_DB_PASS", "SUPERSECRET"); err != nil {
		t.Fatalf("SetRepoVar() error = %v", err)
	}

	var blob []byte
	if err := s.db.QueryRow(`SELECT value FROM repo_vars WHERE repo_id = ?`, r.ID).Scan(&blob); err != nil {
		t.Fatalf("reading raw value: %v", err)
	}
	if bytes.Contains(blob, []byte("SUPERSECRET")) {
		t.Error("value stored in plaintext")
	}

	got, err := s.RepoVars(r.ID)
	if err != nil {
		t.Fatalf("RepoVars() error = %v", err)
	}
	if got["_PRIZM_DB_PASS"] != "SUPERSECRET" {
		t.Errorf("decrypted value = %q, want %q", got["_PRIZM_DB_PASS"], "SUPERSECRET")
	}

	// Metadata must stay queryable in plaintext for fast completion.
	var name string
	if err := s.db.QueryRow(`SELECT name FROM repos WHERE id = ?`, r.ID).Scan(&name); err != nil {
		t.Fatalf("reading repo name: %v", err)
	}
	if name != "backend" {
		t.Errorf("repo name = %q, want plaintext %q", name, "backend")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestRepoVars|TestSetVar|TestStoreKeeps|TestSharedGroup|TestValues'`
Expected: FAIL — `s.SetRepoVar undefined`.

- [ ] **Step 3: Implement the variable layers**

Create `internal/store/vars.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidKey is returned for a key that is not a legal env-var name.
var ErrInvalidKey = errors.New("invalid variable key")

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%q: %w (must match [A-Za-z_][A-Za-z0-9_]*)", key, ErrInvalidKey)
	}
	return nil
}

// SharedGroup is a named bag of vars scoped to (workflow, repo subset).
type SharedGroup struct {
	ID         int64
	WorkflowID int64
	Name       string
}

// ---- Layer 1: repo-shared -------------------------------------------------

// SetRepoVar upserts a variable that applies in every workflow touching this repo.
func (s *Store) SetRepoVar(repoID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO repo_vars(repo_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(repo_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		repoID, key, blob, time.Now().Unix())
	return err
}

// RepoVars returns the repo-shared layer, decrypted. Values are templates.
func (s *Store) RepoVars(repoID int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM repo_vars WHERE repo_id = ?`, repoID)
}

// ---- Layer 2: workflow-scoped shared groups -------------------------------

// CreateSharedGroup creates a named shared-variable group inside a workflow.
func (s *Store) CreateSharedGroup(workflowID int64, name string) (SharedGroup, error) {
	res, err := s.db.Exec(
		`INSERT INTO shared_groups(workflow_id, name, created_at) VALUES (?, ?, ?)`,
		workflowID, name, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return SharedGroup{}, fmt.Errorf("shared group %q: %w", name, ErrExists)
		}
		return SharedGroup{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SharedGroup{}, err
	}
	return SharedGroup{ID: id, WorkflowID: workflowID, Name: name}, nil
}

// SharedGroupByName looks a shared group up within a workflow.
func (s *Store) SharedGroupByName(workflowID int64, name string) (SharedGroup, error) {
	var sg SharedGroup
	err := s.db.QueryRow(
		`SELECT id, workflow_id, name FROM shared_groups WHERE workflow_id = ? AND name = ?`,
		workflowID, name,
	).Scan(&sg.ID, &sg.WorkflowID, &sg.Name)

	if errors.Is(err, sql.ErrNoRows) {
		return SharedGroup{}, fmt.Errorf("shared group %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return SharedGroup{}, err
	}
	return sg, nil
}

// AddSharedGroupRepo makes a repo a member of a shared group. Idempotent.
func (s *Store) AddSharedGroupRepo(sharedGroupID, repoID int64) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO shared_group_repos(shared_group_id, repo_id) VALUES (?, ?)`,
		sharedGroupID, repoID,
	)
	return err
}

// SetSharedGroupVar upserts a variable in a shared group.
func (s *Store) SetSharedGroupVar(sharedGroupID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO shared_group_vars(shared_group_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(shared_group_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		sharedGroupID, key, blob, time.Now().Unix())
	return err
}

// SharedGroupVars returns one shared group's variables, decrypted.
func (s *Store) SharedGroupVars(sharedGroupID int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM shared_group_vars WHERE shared_group_id = ?`, sharedGroupID)
}

// SharedGroupsForRepo returns the shared groups this repo belongs to within a
// workflow, ordered by name. Order is deterministic so that two groups
// defining the same key resolve the same way every time (later name wins).
func (s *Store) SharedGroupsForRepo(workflowID, repoID int64) ([]SharedGroup, error) {
	rows, err := s.db.Query(`
		SELECT sg.id, sg.workflow_id, sg.name
		FROM shared_groups sg
		JOIN shared_group_repos sgr ON sgr.shared_group_id = sg.id
		WHERE sg.workflow_id = ? AND sgr.repo_id = ?
		ORDER BY sg.name`, workflowID, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedGroup
	for rows.Next() {
		var sg SharedGroup
		if err := rows.Scan(&sg.ID, &sg.WorkflowID, &sg.Name); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// ---- Layer 3: repo + workflow specific ------------------------------------

// SetWorkflowRepoVar upserts the highest-precedence layer.
func (s *Store) SetWorkflowRepoVar(workflowID, repoID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO workflow_repo_vars(workflow_id, repo_id, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workflow_id, repo_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		workflowID, repoID, key, blob, time.Now().Unix())
	return err
}

// WorkflowRepoVars returns the (workflow, repo) layer, decrypted.
func (s *Store) WorkflowRepoVars(workflowID, repoID int64) (map[string]string, error) {
	return s.queryVars(
		`SELECT key, value FROM workflow_repo_vars WHERE workflow_id = ? AND repo_id = ?`,
		workflowID, repoID,
	)
}

// ---- shared helper --------------------------------------------------------

func (s *Store) queryVars(query string, args ...any) (map[string]string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var (
			key  string
			blob []byte
		)
		if err := rows.Scan(&key, &blob); err != nil {
			return nil, err
		}
		value, err := s.cipher.Decrypt(blob)
		if err != nil {
			return nil, fmt.Errorf("decrypting %q: %w", key, err)
		}
		out[key] = value
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS — every store test, including `TestValuesAreEncryptedAtRest`.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): three variable layers, shared groups, encryption at rest"
```

---

### Task 9: Resolver — merge, interpolate, strip internals

**Files:**
- Create: `internal/resolve/merge.go`, `internal/resolve/expand.go`, `internal/resolve/resolve.go`
- Test: `internal/resolve/merge_test.go`, `internal/resolve/expand_test.go`, `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `store.Store` and every getter from Task 8; `store.Repo`, `store.Workflow`.
- Produces:
  - `resolve.InternalPrefix` — the constant `"_PRIZM_"`.
  - `resolve.IsInternal(key string) bool`
  - `resolve.Layer` struct: `Name string`, `Vars map[string]string` — `Name` appears only in error messages.
  - `resolve.Merge(layers []Layer) map[string]string` — later layers win.
  - `resolve.Expand(vars map[string]string) (map[string]string, error)` — resolves `${NAME}` references recursively. Errors: `resolve.ErrUnresolved`, `resolve.ErrCycle`.
  - `resolve.Emit(expanded map[string]string) map[string]string` — drops every `_PRIZM_`-prefixed key, leaving exactly what gets written to disk.
  - `resolve.ForRepo(s *store.Store, wf store.Workflow, repo store.Repo) (map[string]string, error)` — assembles the three layers in precedence order and merges them, returning **templates**, so a later phase's `sync` can compare templates rather than expansions.

Three pure functions rather than one: `up` needs `Emit(Expand(Merge(...)))`, but `sync` and `audit` (later phases) need to stop at `Merge` and compare templates. Keeping the stages separate is what makes both possible.

- [ ] **Step 1: Write the failing merge test**

Create `internal/resolve/merge_test.go`:

```go
package resolve

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMergePrecedenceLaterLayerWins(t *testing.T) {
	got := Merge([]Layer{
		{Name: "repo-shared", Vars: map[string]string{"A": "1", "B": "repo"}},
		{Name: "shared:db", Vars: map[string]string{"B": "shared", "C": "3"}},
		{Name: "workflow+repo", Vars: map[string]string{"C": "specific"}},
	})

	want := map[string]string{"A": "1", "B": "shared", "C": "specific"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Merge() mismatch (-want +got):\n%s", diff)
	}
}

func TestMergeEmptyLayers(t *testing.T) {
	if got := Merge(nil); len(got) != 0 {
		t.Errorf("Merge(nil) = %v, want empty map", got)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	first := map[string]string{"A": "1"}
	Merge([]Layer{{Name: "a", Vars: first}, {Name: "b", Vars: map[string]string{"A": "2"}}})

	if first["A"] != "1" {
		t.Errorf("input layer was mutated: A = %q, want %q", first["A"], "1")
	}
}

func TestIsInternal(t *testing.T) {
	tests := map[string]bool{
		"_PRIZM_DB_URL": true,
		"_PRIZM_":       true,
		"DB_URL":      false,
		"PRIZM_DB_URL":  false,
		"_DB_URL":     false,
		"_prizm_db_url": false, // the prefix is case-sensitive
	}
	for key, want := range tests {
		if got := IsInternal(key); got != want {
			t.Errorf("IsInternal(%q) = %v, want %v", key, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolve/`
Expected: FAIL — `undefined: Merge`, `undefined: Layer`, `undefined: IsInternal`.

- [ ] **Step 3: Implement Merge**

Create `internal/resolve/merge.go`:

```go
// Package resolve turns prizm's stored variable layers into the exact map that
// gets written to a repo's env file.
package resolve

import "strings"

// InternalPrefix marks a variable that exists only as plumbing: it can be
// referenced from any template but is never written to a repo's env file.
const InternalPrefix = "_PRIZM_"

// IsInternal reports whether key is prizm-internal.
func IsInternal(key string) bool { return strings.HasPrefix(key, InternalPrefix) }

// Layer is one contributor to a repo's variables. Name appears in errors.
type Layer struct {
	Name string
	Vars map[string]string
}

// Merge folds layers low-precedence first; later layers win.
func Merge(layers []Layer) map[string]string {
	out := make(map[string]string)
	for _, layer := range layers {
		for key, value := range layer.Vars {
			out[key] = value
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/resolve/ -run 'TestMerge|TestIsInternal' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing expansion test**

Create `internal/resolve/expand_test.go`:

```go
package resolve

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExpandSimpleReference(t *testing.T) {
	got, err := Expand(map[string]string{
		"HOST": "localhost",
		"URL":  "http://${HOST}:8080",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	want := map[string]string{"HOST": "localhost", "URL": "http://localhost:8080"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Expand() mismatch (-want +got):\n%s", diff)
	}
}

// The spec's shape: a public name pointing at internal plumbing that is
// itself derived from two more internal values.
func TestExpandNestedInternalDerivation(t *testing.T) {
	got, err := Expand(map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_PASS": "hunter2",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app",
		"DB_URL":       "${_PRIZM_DB_URL}",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	if want := "postgres://svc_app:hunter2@localhost:5432/app"; got["DB_URL"] != want {
		t.Errorf("DB_URL = %q, want %q", got["DB_URL"], want)
	}
}

func TestExpandChains(t *testing.T) {
	got, err := Expand(map[string]string{
		"USER": "u",
		"URL":  "postgres://${USER}@h/db",
		"DSN":  "${URL}?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if want := "postgres://u@h/db?sslmode=disable"; got["DSN"] != want {
		t.Errorf("DSN = %q, want %q", got["DSN"], want)
	}
}

func TestExpandMultipleReferencesInOneValue(t *testing.T) {
	got, err := Expand(map[string]string{"A": "1", "B": "2", "SUM": "${A}-${B}-${A}"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if got["SUM"] != "1-2-1" {
		t.Errorf("SUM = %q, want %q", got["SUM"], "1-2-1")
	}
}

func TestExpandLeavesBareDollarAlone(t *testing.T) {
	// Passwords and regexes are full of $. Only ${...} is a reference.
	in := map[string]string{"PASS": "p$ssw0rd$", "REGEX": "^foo$", "COST": "$100"}

	got, err := Expand(in)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("Expand() mismatch (-want +got):\n%s", diff)
	}
}

func TestExpandEscapedDollarBrace(t *testing.T) {
	got, err := Expand(map[string]string{"TEMPLATE": "literal $${NOT_A_REF} here"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if want := "literal ${NOT_A_REF} here"; got["TEMPLATE"] != want {
		t.Errorf("TEMPLATE = %q, want %q", got["TEMPLATE"], want)
	}
}

func TestExpandUnresolvedReferenceIsAnError(t *testing.T) {
	_, err := Expand(map[string]string{"DB_URL": "postgres://${_PRIZM_DB_USER}@h/db"})
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Expand() error = %v, want ErrUnresolved", err)
	}
	if !strings.Contains(err.Error(), "_PRIZM_DB_USER") || !strings.Contains(err.Error(), "DB_URL") {
		t.Errorf("error = %q, want it to name both the referencing key and the missing one", err)
	}
}

func TestExpandDetectsCycles(t *testing.T) {
	_, err := Expand(map[string]string{"A": "${B}", "B": "${A}"})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("Expand() error = %v, want ErrCycle", err)
	}
}

func TestExpandDetectsSelfReference(t *testing.T) {
	if _, err := Expand(map[string]string{"A": "${A}"}); !errors.Is(err, ErrCycle) {
		t.Errorf("Expand() error = %v, want ErrCycle", err)
	}
}

func TestExpandIsDeterministic(t *testing.T) {
	in := map[string]string{"A": "1", "B": "${A}", "C": "${B}", "D": "${C}"}

	first, err := Expand(in)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := Expand(in)
		if err != nil {
			t.Fatalf("Expand() error = %v", err)
		}
		if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("Expand() not deterministic on run %d (-first +got):\n%s", i, diff)
		}
	}
}

// The output file must be opaque: no trace of the derivation chain.
func TestEmitDropsInternalKeys(t *testing.T) {
	got := Emit(map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_PASS": "hunter2",
		"_PRIZM_DB_URL":  "postgres://svc_app:hunter2@h/db",
		"DB_URL":       "postgres://svc_app:hunter2@h/db",
		"PORT":         "8080",
	})

	want := map[string]string{"DB_URL": "postgres://svc_app:hunter2@h/db", "PORT": "8080"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Emit() mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/resolve/ -run 'TestExpand|TestEmit'`
Expected: FAIL — `undefined: Expand`, `undefined: ErrUnresolved`.

- [ ] **Step 7: Implement Expand and Emit**

Create `internal/resolve/expand.go`:

```go
package resolve

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Expansion errors.
var (
	// ErrUnresolved means a template referenced a key that is not defined.
	ErrUnresolved = errors.New("unresolved reference")
	// ErrCycle means references form a loop.
	ErrCycle = errors.New("reference cycle")
)

// escapeSentinel stands in for `$${` while a value is being expanded, so an
// escaped reference is never mistaken for a real one.
const escapeSentinel = "\x00PRIZM_ESCAPED_DOLLAR\x00"

// refPattern matches ${NAME}. A lone `$` is always literal.
var refPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand resolves every ${NAME} reference against vars itself. Keys are
// processed in sorted order so errors are reported deterministically.
func Expand(vars map[string]string) (map[string]string, error) {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(vars))
	for _, key := range keys {
		v, err := expandOne(key, vars, out, nil)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// expandOne resolves a single key, recursing into its references. `chain`
// carries the path taken so a cycle can be reported by name.
func expandOne(key string, vars map[string]string, done map[string]string, chain []string) (string, error) {
	if v, ok := done[key]; ok {
		return v, nil
	}

	for _, seen := range chain {
		if seen == key {
			return "", fmt.Errorf("%w: %s → %s", ErrCycle, strings.Join(chain, " → "), key)
		}
	}
	chain = append(chain, key)

	raw := strings.ReplaceAll(vars[key], "$${", escapeSentinel)

	var expandErr error
	result := refPattern.ReplaceAllStringFunc(raw, func(match string) string {
		if expandErr != nil {
			return ""
		}
		ref := refPattern.FindStringSubmatch(match)[1]

		if _, ok := vars[ref]; !ok {
			expandErr = fmt.Errorf("%w: %s references ${%s}, which is not defined", ErrUnresolved, key, ref)
			return ""
		}

		value, err := expandOne(ref, vars, done, chain)
		if err != nil {
			expandErr = err
			return ""
		}
		return value
	})
	if expandErr != nil {
		return "", expandErr
	}

	result = strings.ReplaceAll(result, escapeSentinel, "${")
	done[key] = result
	return result, nil
}

// Emit returns only the variables that belong in a repo's env file: every
// expanded value except the internal plumbing. What lands on disk carries no
// trace of where it was derived from.
func Emit(expanded map[string]string) map[string]string {
	out := make(map[string]string, len(expanded))
	for key, value := range expanded {
		if IsInternal(key) {
			continue
		}
		out[key] = value
	}
	return out
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/resolve/ -v`
Expected: PASS — all merge, expand and emit tests.

- [ ] **Step 9: Write the failing layer-assembly test**

Create `internal/resolve/resolve_test.go`:

```go
package resolve

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// envFor runs the full pipeline the way `up` will.
func envFor(t *testing.T, s *store.Store, wf store.Workflow, repo store.Repo) map[string]string {
	t.Helper()

	vars, err := ForRepo(s, wf, repo)
	if err != nil {
		t.Fatalf("ForRepo(%s) error = %v", repo.Name, err)
	}
	expanded, err := Expand(vars)
	if err != nil {
		t.Fatalf("Expand(%s) error = %v", repo.Name, err)
	}
	return Emit(expanded)
}

// The spec's worked example: backend, auth and ai share derived DB credentials
// and expose them under their own names; frontend is in the same workflow but
// not in the shared group, so it sees none of it.
func TestForRepoAssemblesAllThreeLayers(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "local", []int64{fe.ID, be.ID, auth.ID})

	// Layer 1: repo-shared.
	s.SetRepoVar(be.ID, "LOG_LEVEL", "debug")

	// Layer 2: shared group covering backend + auth only, all internal.
	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	// Layer 3: each repo names the shared value whatever its own code expects.
	s.SetWorkflowRepoVar(wf.ID, be.ID, "PORT", "8080")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, auth.ID, "DATABASE_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, fe.ID, "API_URL", "http://localhost:8080")

	dsn := "postgres://svc_app:hunter2@localhost:5432/app"

	want := map[string]map[string]string{
		"backend":  {"LOG_LEVEL": "debug", "PORT": "8080", "DB_URL": dsn},
		"auth":     {"DATABASE_URL": dsn},
		"frontend": {"API_URL": "http://localhost:8080"},
	}
	for _, repo := range []store.Repo{be, auth, fe} {
		if diff := cmp.Diff(want[repo.Name], envFor(t, s, wf, repo)); diff != "" {
			t.Errorf("%s env mismatch (-want +got):\n%s", repo.Name, diff)
		}
	}
}

// A repo overriding one input of a shared template changes only its own
// expansion; the stored template is untouched for everyone else.
func TestForRepoOverrideOfTemplateInput(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID})

	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}@h/db")

	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, auth.ID, "DB_URL", "${_PRIZM_DB_URL}")
	// backend pins its own user.
	s.SetWorkflowRepoVar(wf.ID, be.ID, "_PRIZM_DB_USER", "svc_backend")

	if got := envFor(t, s, wf, be)["DB_URL"]; got != "postgres://svc_backend@h/db" {
		t.Errorf("backend DB_URL = %q, want the overridden user", got)
	}
	if got := envFor(t, s, wf, auth)["DB_URL"]; got != "postgres://svc_app@h/db" {
		t.Errorf("auth DB_URL = %q, want the shared user", got)
	}
	// The override is still _PRIZM_-named, so it cannot leak into the file.
	if _, leaked := envFor(t, s, wf, be)["_PRIZM_DB_USER"]; leaked {
		t.Error("internal var leaked into backend's emitted env")
	}
}

func TestForRepoReturnsTemplatesNotExpansions(t *testing.T) {
	// sync/audit compare templates, so ForRepo must not expand.
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.SetWorkflowRepoVar(wf.ID, r.ID, "_PRIZM_HOST", "h")
	s.SetWorkflowRepoVar(wf.ID, r.ID, "URL", "http://${_PRIZM_HOST}")

	vars, err := ForRepo(s, wf, r)
	if err != nil {
		t.Fatalf("ForRepo() error = %v", err)
	}
	if vars["URL"] != "http://${_PRIZM_HOST}" {
		t.Errorf("URL = %q, want the unexpanded template", vars["URL"])
	}
}

func TestForRepoWithNoVarsReturnsEmpty(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "solo", "/code/solo", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	vars, err := ForRepo(s, wf, r)
	if err != nil {
		t.Fatalf("ForRepo() error = %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("ForRepo() = %v, want empty", vars)
	}
}
```

- [ ] **Step 10: Run test to verify it fails**

Run: `go test ./internal/resolve/ -run TestForRepo`
Expected: FAIL — `undefined: ForRepo`.

- [ ] **Step 11: Implement ForRepo**

Create `internal/resolve/resolve.go`:

```go
package resolve

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// ForRepo assembles a repo's variable layers, lowest precedence first, and
// merges them. The result still contains ${...} templates; call Expand to
// resolve them and Emit to drop the internal plumbing.
func ForRepo(s *store.Store, wf store.Workflow, repo store.Repo) (map[string]string, error) {
	repoVars, err := s.RepoVars(repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading repo-shared vars for %q: %w", repo.Name, err)
	}
	layers := []Layer{{Name: "repo-shared", Vars: repoVars}}

	sharedGroups, err := s.SharedGroupsForRepo(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading shared groups for %q: %w", repo.Name, err)
	}
	for _, sg := range sharedGroups {
		vars, err := s.SharedGroupVars(sg.ID)
		if err != nil {
			return nil, fmt.Errorf("reading shared group %q: %w", sg.Name, err)
		}
		layers = append(layers, Layer{Name: "shared:" + sg.Name, Vars: vars})
	}

	specific, err := s.WorkflowRepoVars(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s vars: %w", wf.Name, repo.Name, err)
	}
	layers = append(layers, Layer{Name: wf.Name + "+" + repo.Name, Vars: specific})

	return Merge(layers), nil
}
```

- [ ] **Step 12: Run the whole package**

Run: `go test ./internal/resolve/ -v`
Expected: PASS — every merge, expand, emit and layer-assembly test.

- [ ] **Step 13: Commit**

```bash
git add internal/resolve/
git commit -m "feat(resolve): layer merge, \${VAR} interpolation, internal-var stripping"
```

---

### Task 10: Directory-aware ranking for completion

**Files:**
- Create: `internal/rank/rank.go`
- Test: `internal/rank/rank_test.go`

**Interfaces:**
- Consumes: nothing (pure package, stdlib only).
- Produces:
  - `rank.Candidate` struct: `Name string`, `Paths []string`, `UseCount int`, `LastUsedAt time.Time`.
  - `rank.Rank(candidates []Candidate, cwd string, now time.Time) []string` — returns names most-relevant first.

The spec is explicit: **sort, not filter.** Every group is always returned; only the order changes. Ranking, highest first:

1. `cwd` is inside one of the candidate's repo paths — the group you are standing in wins. Among several matches, the longest (deepest) path wins, so a nested repo beats its parent.
2. `cwd` is a parent of one of the candidate's repo paths — you are above the repos, still probably what you meant.
3. Frecency, `zoxide`-style: `useCount × decay(age)` where decay is 4 within the hour, 2 within the day, 0.5 within the week, 0.25 beyond.
4. Name ascending, so output is stable when scores tie.

- [ ] **Step 1: Write the failing test**

Create `internal/rank/rank_test.go`:

```go
package rank

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var now = time.Unix(1700000000, 0)

func TestRankPutsContainingGroupFirst(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "alpha", Paths: []string{"/code/alpha"}, UseCount: 100, LastUsedAt: now},
		{Name: "xyz", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz/backend/src", now)

	// xyz wins on containment even though alpha has far better frecency.
	if diff := cmp.Diff([]string{"xyz", "alpha"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankNeverFilters(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "a", Paths: []string{"/code/a"}},
		{Name: "b", Paths: []string{"/code/b"}},
		{Name: "c", Paths: []string{"/code/c"}},
	}, "/somewhere/else", now)

	if len(got) != 3 {
		t.Errorf("Rank() returned %d candidates, want all 3 — it must sort, not filter", len(got))
	}
}

func TestRankDeepestContainingPathWins(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "outer", Paths: []string{"/code"}},
		{Name: "inner", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz/backend/src", now)

	if got[0] != "inner" {
		t.Errorf("Rank()[0] = %q, want %q — the deeper match should win", got[0], "inner")
	}
}

func TestRankExactPathMatchCounts(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "other", Paths: []string{"/code/other"}, UseCount: 50, LastUsedAt: now},
		{Name: "xyz", Paths: []string{"/code/xyz"}},
	}, "/code/xyz", now)

	if got[0] != "xyz" {
		t.Errorf("Rank()[0] = %q, want %q for an exact cwd match", got[0], "xyz")
	}
}

func TestRankParentOfRepoBeatsUnrelated(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "unrelated", Paths: []string{"/elsewhere/x"}},
		{Name: "xyz", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz", now)

	if got[0] != "xyz" {
		t.Errorf("Rank()[0] = %q, want %q — cwd is a parent of xyz's repo", got[0], "xyz")
	}
}

func TestRankSiblingDirectoryIsNotContainment(t *testing.T) {
	// /code/xyz-old must not count as being inside /code/xyz.
	got := Rank([]Candidate{
		{Name: "xyz", Paths: []string{"/code/xyz"}},
		{Name: "recent", Paths: []string{"/elsewhere"}, UseCount: 10, LastUsedAt: now},
	}, "/code/xyz-old", now)

	if got[0] != "recent" {
		t.Errorf("Rank()[0] = %q, want %q — prefix match must respect path boundaries", got[0], "recent")
	}
}

func TestRankFrecencyOrdersUnrelatedGroups(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "stale", Paths: []string{"/a"}, UseCount: 50, LastUsedAt: now.Add(-30 * 24 * time.Hour)},
		{Name: "hot", Paths: []string{"/b"}, UseCount: 5, LastUsedAt: now.Add(-10 * time.Minute)},
		{Name: "never", Paths: []string{"/c"}},
	}, "/somewhere/else", now)

	if diff := cmp.Diff([]string{"hot", "stale", "never"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankTiesBreakByName(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "zeta", Paths: []string{"/z"}},
		{Name: "alpha", Paths: []string{"/a"}},
		{Name: "mid", Paths: []string{"/m"}},
	}, "/somewhere/else", now)

	if diff := cmp.Diff([]string{"alpha", "mid", "zeta"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankHandlesEmptyInput(t *testing.T) {
	if got := Rank(nil, "/anywhere", now); len(got) != 0 {
		t.Errorf("Rank(nil) = %v, want empty", got)
	}
}

func TestRankHandlesCandidateWithNoPaths(t *testing.T) {
	got := Rank([]Candidate{{Name: "empty"}}, "/anywhere", now)
	if diff := cmp.Diff([]string{"empty"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rank/`
Expected: FAIL — `undefined: Rank`, `undefined: Candidate`.

- [ ] **Step 3: Implement Rank**

Create `internal/rank/rank.go`:

```go
// Package rank orders groups by how relevant they are to the current
// directory. It always sorts and never filters: every candidate comes back.
package rank

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Candidate is one group being ranked.
type Candidate struct {
	Name       string
	Paths      []string
	UseCount   int
	LastUsedAt time.Time
}

// Score bands. Containment dominates frecency by construction: standing in a
// repo is a far stronger signal than having used something a lot last week.
const (
	insideRepoBase = 1_000_000_000.0
	parentOfRepo   = 1_000_000.0
)

// Rank returns candidate names, most relevant first.
func Rank(candidates []Candidate, cwd string, now time.Time) []string {
	cwd = filepath.Clean(cwd)

	type scored struct {
		name  string
		score float64
	}

	out := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, scored{name: c.Name, score: score(c, cwd, now)})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})

	names := make([]string, 0, len(out))
	for _, s := range out {
		names = append(names, s.name)
	}
	return names
}

func score(c Candidate, cwd string, now time.Time) float64 {
	best := 0.0

	for _, p := range c.Paths {
		p = filepath.Clean(p)
		switch {
		case contains(p, cwd):
			// Deeper match wins, so a nested repo beats its parent directory.
			if s := insideRepoBase + float64(len(p)); s > best {
				best = s
			}
		case contains(cwd, p):
			if parentOfRepo > best {
				best = parentOfRepo
			}
		}
	}
	if best > 0 {
		return best
	}

	return float64(c.UseCount) * decay(now.Sub(c.LastUsedAt))
}

// contains reports whether child is dir itself or lives beneath it. It
// compares whole path segments, so /code/xyz does not contain /code/xyz-old.
func contains(dir, child string) bool {
	return child == dir || strings.HasPrefix(child, dir+string(filepath.Separator))
}

// decay is the zoxide-style frecency curve.
func decay(age time.Duration) float64 {
	switch {
	case age < time.Hour:
		return 4
	case age < 24*time.Hour:
		return 2
	case age < 7*24*time.Hour:
		return 0.5
	default:
		return 0.25
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/rank/ -v`
Expected: PASS — all ten tests.

- [ ] **Step 5: Commit**

```bash
git add internal/rank/
git commit -m "feat(rank): directory-aware and frecency ordering for completion"
```

---

### Task 11: Apply — build the file and swap the symlink

**Files:**
- Create: `internal/apply/link.go`
- Test: `internal/apply/link_test.go`

**Interfaces:**
- Consumes: `config.EnsureDir` (Task 1).
- Produces:
  - `apply.Result` struct: `BuiltPath string`, `LinkPath string`, `BackedUpTo string` (empty when nothing needed backing up).
  - `apply.Apply(builtPath, content, repoPath, envFile string, now time.Time) (Result, error)`

Behaviour, in order:

1. Write `content` to `builtPath` (mode `0600`) atomically — temp file in the same directory, then `rename`.
2. Look at `repoPath/envFile`:
   - absent → nothing to preserve;
   - a symlink → prizm (or something like it) owns it; it will simply be replaced;
   - a **regular file** → someone's real env file. Rename it to `<envFile>.prizm-backup.<YYYYMMDD-HHMMSS>` first. This is the spec's "backup before overwrite" insurance, and it is in the core phase precisely because `up` writes into directories people are actively working in.
3. Create the symlink atomically: make it under a temp name in the repo directory, then `rename` over the target. There is never a window where the repo has no env file.

A missing `repoPath` is an error naming the repo, since the spec makes repo paths a stable contract and moving one is what breaks it.

- [ ] **Step 1: Write the failing test**

Create `internal/apply/link_test.go`:

```go
package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 27, 14, 30, 0, 0, time.UTC)

type fixture struct {
	builtPath string
	repoPath  string
	linkPath  string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	return fixture{
		builtPath: filepath.Join(root, "built", "XYZ", "local", "backend.env"),
		repoPath:  repoPath,
		linkPath:  filepath.Join(repoPath, ".env"),
	}
}

func TestApplyWritesBuiltFileAndSymlinks(t *testing.T) {
	f := newFixture(t)

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", now)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if res.LinkPath != f.linkPath {
		t.Errorf("LinkPath = %q, want %q", res.LinkPath, f.linkPath)
	}
	if res.BackedUpTo != "" {
		t.Errorf("BackedUpTo = %q, want empty", res.BackedUpTo)
	}

	got, err := os.ReadFile(f.builtPath)
	if err != nil {
		t.Fatalf("reading built file: %v", err)
	}
	if string(got) != "A=1\n" {
		t.Errorf("built content = %q, want %q", got, "A=1\n")
	}

	info, err := os.Lstat(f.linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("target is not a symlink")
	}

	dest, err := os.Readlink(f.linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if dest != f.builtPath {
		t.Errorf("symlink points at %q, want %q", dest, f.builtPath)
	}
}

func TestApplyBuiltFileIsOwnerOnly(t *testing.T) {
	f := newFixture(t)

	if _, err := Apply(f.builtPath, "SECRET=x\n", f.repoPath, ".env", now); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	info, err := os.Stat(f.builtPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("built file mode = %04o, want 0600", perm)
	}
}

func TestApplyBacksUpAnExistingRegularFile(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.linkPath, []byte("PRECIOUS=keepme\n"), 0o644); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", now)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if res.BackedUpTo == "" {
		t.Fatal("BackedUpTo is empty; the user's real .env was destroyed")
	}
	if !strings.Contains(res.BackedUpTo, ".env.prizm-backup.20260827-143000") {
		t.Errorf("BackedUpTo = %q, want a timestamped .env.prizm-backup.* name", res.BackedUpTo)
	}

	backup, err := os.ReadFile(res.BackedUpTo)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if string(backup) != "PRECIOUS=keepme\n" {
		t.Errorf("backup content = %q, want the original file", backup)
	}
}

func TestApplyReplacesAnExistingSymlinkWithoutBackup(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "other.env")
	os.WriteFile(other, []byte("OLD=1\n"), 0o600)
	if err := os.Symlink(other, f.linkPath); err != nil {
		t.Fatalf("seeding symlink: %v", err)
	}

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", now)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.BackedUpTo != "" {
		t.Errorf("BackedUpTo = %q, want empty — a symlink needs no backup", res.BackedUpTo)
	}

	dest, _ := os.Readlink(f.linkPath)
	if dest != f.builtPath {
		t.Errorf("symlink points at %q, want %q", dest, f.builtPath)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	f := newFixture(t)

	for i := 0; i < 3; i++ {
		res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", now)
		if err != nil {
			t.Fatalf("Apply() run %d error = %v", i, err)
		}
		if res.BackedUpTo != "" {
			t.Errorf("run %d backed up %q; re-applying must not accumulate backups", i, res.BackedUpTo)
		}
	}

	entries, _ := os.ReadDir(f.repoPath)
	if len(entries) != 1 {
		t.Errorf("repo dir has %d entries, want exactly 1 (.env)", len(entries))
	}
}

func TestApplyHonoursACustomEnvFileName(t *testing.T) {
	f := newFixture(t)

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env.local", now)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	want := filepath.Join(f.repoPath, ".env.local")
	if res.LinkPath != want {
		t.Errorf("LinkPath = %q, want %q", res.LinkPath, want)
	}
	if _, err := os.Lstat(want); err != nil {
		t.Errorf("expected %q to exist: %v", want, err)
	}
}

func TestApplyErrorsWhenRepoPathIsMissing(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := Apply(f.builtPath, "A=1\n", missing, ".env", now)
	if err == nil {
		t.Fatal("Apply(missing repo) error = nil, want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name the missing path", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apply/`
Expected: FAIL — `undefined: Apply`.

- [ ] **Step 3: Implement Apply**

Create `internal/apply/link.go`:

```go
// Package apply writes a resolved env file and points a repo at it.
package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/troglodytto/prizm/internal/config"
)

// backupStamp is the suffix format for a displaced env file.
const backupStamp = "20060102-150405"

// Result describes what Apply did, for reporting back to the user.
type Result struct {
	BuiltPath  string
	LinkPath   string
	BackedUpTo string // empty unless a real file was displaced
}

// Apply writes content to builtPath and points repoPath/envFile at it.
func Apply(builtPath, content, repoPath, envFile string, now time.Time) (Result, error) {
	res := Result{BuiltPath: builtPath, LinkPath: filepath.Join(repoPath, envFile)}

	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("repo path %s is missing or not a directory", repoPath)
	}

	if err := config.EnsureDir(filepath.Dir(builtPath)); err != nil {
		return Result{}, fmt.Errorf("creating build directory: %w", err)
	}
	if err := writeFileAtomic(builtPath, content); err != nil {
		return Result{}, fmt.Errorf("writing %s: %w", builtPath, err)
	}

	backup, err := preserveExisting(res.LinkPath, now)
	if err != nil {
		return Result{}, err
	}
	res.BackedUpTo = backup

	if err := symlinkAtomic(builtPath, res.LinkPath); err != nil {
		return Result{}, fmt.Errorf("linking %s: %w", res.LinkPath, err)
	}
	return res, nil
}

// writeFileAtomic writes via a temp file in the same directory, then renames.
func writeFileAtomic(path, content string) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".prizm-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// preserveExisting moves a real file out of the way and returns where it went.
// Symlinks are left for the rename to replace; absent targets are a no-op.
func preserveExisting(target string, now time.Time) (string, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}

	backup := fmt.Sprintf("%s.prizm-backup.%s", target, now.Format(backupStamp))
	if err := os.Rename(target, backup); err != nil {
		return "", fmt.Errorf("backing up %s: %w", target, err)
	}
	return backup, nil
}

// symlinkAtomic creates the link under a temp name, then renames it into
// place, so the repo never briefly lacks an env file.
func symlinkAtomic(dest, target string) error {
	tmp := filepath.Join(filepath.Dir(target), fmt.Sprintf(".prizm-link-%d", os.Getpid()))
	_ = os.Remove(tmp)

	if err := os.Symlink(dest, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apply/ -v`
Expected: PASS — all seven tests.

- [ ] **Step 5: Commit**

```bash
git add internal/apply/
git commit -m "feat(apply): atomic built-file write, backup-before-overwrite, symlink swap"
```

---

### Task 12: Output style — one glyph set, one palette, one column

**Files:**
- Create: `internal/style/style.go`
- Test: `internal/style/style_test.go`

**Interfaces:**
- Consumes: nothing but `github.com/charmbracelet/lipgloss`.
- Produces:
  - `style.Mark` (`style.OK`, `Fail`, `Warn`, `Add`, `Remove`, `Change`, `Same`, `Ask`) with `(Mark).Glyph() string`.
  - `style.NameWidth` — the column width every status line aligns its subject in.
  - `style.Row(m Mark, name, detail string) string` — `"✓ backend        set (local)"`.
  - `style.Detail(s string) string`, `style.Heading(s string) string`, `style.Hint(s string) string`, `style.Alert(s string) string`.
  - `style.Tag(tag string) string` and `style.TagColor(tag string) lipgloss.TerminalColor` — the semantic tag palette.
  - `style.Accent`, `Success`, `Danger`, `Caution`, `Neutral` — the palette colours (note `Danger` is the *colour*; the text helper is `Alert`, or they collide), exported so Phase 3's TUI theme builds on them rather than defining a second set.

This task exists because prizm prints status lines from Phase 1 (`up`) through Phase 5 (`status`, containers), and every one of them wants the same shape: a mark, a subject in a fixed column, a dim detail. Left to individual commands that becomes six slightly different column widths and three different ideas of what a warning looks like. It also has to land **before** the first command prints anything, which is the next task.

**Colour degrades on its own.** Lip Gloss detects a non-terminal stdout and honours `NO_COLOR`, so piped output and every test in every phase sees plain text. That is load-bearing: the command tests throughout these plans assert with `strings.Contains` on substrings like `"drift"` and `"backend"`, and they keep working only because nothing injects escape codes when the output is a buffer.

- [ ] **Step 1: Write the failing test**

Create `internal/style/style_test.go`:

```go
package style

import (
	"strings"
	"testing"
)

func TestMarkGlyphs(t *testing.T) {
	tests := map[Mark]string{
		OK:     "✓",
		Fail:   "✗",
		Warn:   "⚠",
		Add:    "+",
		Remove: "-",
		Change: "~",
		Same:   "=",
		Ask:    "?",
	}

	for mark, want := range tests {
		if got := mark.Glyph(); !strings.Contains(got, want) {
			t.Errorf("Mark(%d).Glyph() = %q, want it to contain %q", mark, got, want)
		}
	}
}

func TestMarkGlyphsAreDistinct(t *testing.T) {
	seen := make(map[string]Mark)

	for _, mark := range []Mark{OK, Fail, Warn, Add, Remove, Change, Same, Ask} {
		glyph := mark.Glyph()
		if other, clash := seen[glyph]; clash {
			t.Errorf("Mark(%d) and Mark(%d) both render %q", mark, other, glyph)
		}
		seen[glyph] = mark
	}
}

func TestRowLayout(t *testing.T) {
	got := Row(OK, "backend", "set (local)")

	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("Row() = %q, want it to start with the mark", got)
	}
	if !strings.Contains(got, "backend") || !strings.Contains(got, "set (local)") {
		t.Errorf("Row() = %q, want both the name and the detail", got)
	}
}

// Every status line in every phase must align in the same column.
func TestRowAlignsTheDetailColumn(t *testing.T) {
	short := Row(OK, "ai", "set (local)")
	long := Row(OK, "frontend-app", "set (local)")

	if strings.Index(short, "set (local)") != strings.Index(long, "set (local)") {
		t.Errorf("detail columns do not line up:\n%q\n%q", short, long)
	}
}

// A name longer than the column must push the detail, never be truncated:
// a silently cut repo name is worse than a ragged line.
func TestRowDoesNotTruncateALongName(t *testing.T) {
	name := strings.Repeat("x", NameWidth+8)

	if got := Row(OK, name, "detail"); !strings.Contains(got, name) {
		t.Errorf("Row() = %q, want the full name %q", got, name)
	}
}

func TestRowWithNoDetailHasNoTrailingSpace(t *testing.T) {
	got := Row(Warn, "backend", "")

	if got != strings.TrimRight(got, " ") {
		t.Errorf("Row() = %q, want no trailing whitespace", got)
	}
}

func TestTagColoursAreSemanticAndDistinct(t *testing.T) {
	prod := TagColor("prod")
	qa := TagColor("qa")
	local := TagColor("local")

	if prod == qa || qa == local || prod == local {
		t.Error("tag colours collide; prod must never look like local")
	}
	if TagColor("something-custom") != TagColor("") {
		t.Error("an unknown tag should render like an untagged one")
	}
}

func TestTagRendersTheTagText(t *testing.T) {
	if got := Tag("prod"); !strings.Contains(got, "prod") {
		t.Errorf("Tag() = %q, want the tag text", got)
	}
	if got := Tag(""); got != "" {
		t.Errorf("Tag(\"\") = %q, want empty", got)
	}
}

// Tests and pipes must see plain text, or every substring assertion in every
// phase breaks.
func TestOutputIsPlainWhenNotATerminal(t *testing.T) {
	for _, got := range []string{
		Row(OK, "backend", "set (local)"),
		Heading("XYZ"),
		Detail("dim"),
		Hint("run `prizm sync`"),
		Alert("boom"),
		Tag("prod"),
	} {
		if strings.Contains(got, "\x1b[") {
			t.Errorf("%q contains escape codes; output must be plain off a terminal", got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/style/`
Expected: FAIL — `undefined: Mark`, `undefined: Row`.

- [ ] **Step 3: Implement the style package**

Create `internal/style/style.go`:

```go
// Package style is prizm's single source of visual language. Every user-facing
// line — plain text in Phases 1 and 2, rendered screens from Phase 3 — uses
// these glyphs, colours and widths, so the tool looks like one tool.
//
// Lip Gloss disables colour automatically when the output is not a terminal
// and honours NO_COLOR, so piped output and tests see plain text.
package style

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette. Exported so the Phase 3 TUI theme extends it rather than
// inventing a second set of colours.
var (
	Accent  = lipgloss.AdaptiveColor{Light: "#5A189A", Dark: "#C77DFF"}
	Success = lipgloss.AdaptiveColor{Light: "#1B4332", Dark: "#95D5B2"}
	Danger  = lipgloss.AdaptiveColor{Light: "#9D0208", Dark: "#FF758F"}
	Caution = lipgloss.AdaptiveColor{Light: "#7F5539", Dark: "#E9C46A"}
	Neutral = lipgloss.AdaptiveColor{Light: "#6C757D", Dark: "#6C757D"}
)

// NameWidth is the column every status line aligns its subject in.
const NameWidth = 14

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	detailStyle  = lipgloss.NewStyle().Faint(true)
	hintStyle    = lipgloss.NewStyle().Faint(true)
	dangerStyle  = lipgloss.NewStyle().Bold(true).Foreground(Danger)

	okStyle      = lipgloss.NewStyle().Foreground(Success)
	failStyle    = lipgloss.NewStyle().Bold(true).Foreground(Danger)
	warnStyle    = lipgloss.NewStyle().Foreground(Caution)
	changeStyle  = lipgloss.NewStyle().Foreground(Caution)
	addStyle     = lipgloss.NewStyle().Foreground(Success)
	removeStyle  = lipgloss.NewStyle().Foreground(Danger)
	neutralStyle = lipgloss.NewStyle()
)

// Mark is the leading glyph on a status line.
type Mark int

const (
	// OK is a completed action.
	OK Mark = iota
	// Fail is an action that did not happen.
	Fail
	// Warn is something that happened but deserves attention.
	Warn
	// Add, Remove and Change are diff marks.
	Add
	Remove
	Change
	// Same is an unchanged item.
	Same
	// Ask is something prizm will not decide on its own.
	Ask
)

// Glyph renders the mark.
func (m Mark) Glyph() string {
	switch m {
	case OK:
		return okStyle.Render("✓")
	case Fail:
		return failStyle.Render("✗")
	case Warn:
		return warnStyle.Render("⚠")
	case Add:
		return addStyle.Render("+")
	case Remove:
		return removeStyle.Render("-")
	case Change:
		return changeStyle.Render("~")
	case Same:
		return neutralStyle.Render("=")
	case Ask:
		return warnStyle.Render("?")
	}
	return " "
}

// Row is the standard status line: a mark, a subject padded to NameWidth, and
// a dim detail. A name longer than the column pushes the detail rather than
// being truncated — a silently cut repo name is worse than a ragged line.
func Row(m Mark, name, detail string) string {
	padded := name
	if pad := NameWidth - lipgloss.Width(name); pad > 0 {
		padded += strings.Repeat(" ", pad)
	}

	line := m.Glyph() + " " + padded
	if detail == "" {
		return strings.TrimRight(line, " ")
	}
	return line + " " + detailStyle.Render(detail)
}

// Heading names a group or a section.
func Heading(s string) string { return headingStyle.Render(s) }

// Detail is secondary text: paths, values, counts.
func Detail(s string) string { return detailStyle.Render(s) }

// Hint is a pointer at the next command to run.
func Hint(s string) string { return hintStyle.Render(s) }

// Alert is for text that should stop someone.
func Alert(s string) string { return dangerStyle.Render(s) }

// tagColors is the semantic palette. Red means production everywhere in prizm:
// in a status line, in a picker badge, and in a prod confirmation.
var tagColors = map[string]lipgloss.TerminalColor{
	"prod":  Danger,
	"qa":    Caution,
	"local": Success,
}

// TagColor returns a tag's colour. Unknown and empty tags share the neutral one.
func TagColor(tag string) lipgloss.TerminalColor {
	if c, ok := tagColors[tag]; ok {
		return c
	}
	return Neutral
}

// Tag renders a workflow tag, or nothing for an untagged workflow.
func Tag(tag string) string {
	if tag == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(TagColor(tag)).Render(tag)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go get github.com/charmbracelet/lipgloss@latest && go test ./internal/style/ -v`
Expected: PASS — all nine tests.

- [ ] **Step 5: Confirm colour actually appears on a terminal**

```bash
cat > /tmp/styledemo.go <<'EOF'
package main

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/style"
)

func main() {
	fmt.Println(style.Heading("XYZ"))
	fmt.Println(style.Row(style.OK, "frontend", "set (local)"))
	fmt.Println(style.Row(style.Fail, "backend", "DB_URL references ${_PRIZM_MISSING}"))
	fmt.Println(style.Row(style.Warn, "auth", "2 local edit(s) will be overwritten"))
	fmt.Println(style.Row(style.Change, "PORT", "8080 → 9090"))
	fmt.Println(style.Tag("prod"), style.Tag("qa"), style.Tag("local"))
	fmt.Println(style.Hint("run `prizm sync` to reconcile"))
}
EOF
go run /tmp/styledemo.go          # colours, aligned column
go run /tmp/styledemo.go | cat    # plain, same alignment
NO_COLOR=1 go run /tmp/styledemo.go
rm /tmp/styledemo.go
```

Expected: the first is coloured, the second and third are plain, and the detail column lines up in all three.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/style/
git commit -m "feat(style): shared glyphs, palette and column widths for all output"
```

**Every later task in every phase uses this.** When a task's code shows `fmt.Fprintf(app.Out, "✓ %-12s ...")` or a bare `⚠`, write `style.Row(style.OK, name, detail)` instead. The rule is simple enough to apply while reading: no command constructs a glyph, a colour, or a column width of its own.

---

### Task 13: CLI — root command, argument rewriting, registration commands

**Files:**
- Create: `internal/cli/root.go`, `internal/cli/rewrite.go`, `internal/cli/group.go`, `internal/cli/repo.go`, `internal/cli/workflow.go`
- Modify: `main.go`
- Test: `internal/cli/rewrite_test.go`, `internal/cli/commands_test.go`

**Interfaces:**
- Consumes: `config.DBPath`, `config.EnsureDir` (Task 1); `crypto.LoadOrCreateKey`, `crypto.NewAESGCM` (Task 3); `store.Open`, `CreateGroup`, `ListGroups`, `GroupByName`, `AddRepo`, `ListRepos`, `AddWorkflow`, `ListWorkflows`, `RepoByName`, `ErrExists`, `ErrNotFound`, `ErrReservedName` (Tasks 4–7).
- Produces:
  - `cli.App` struct: `Store *store.Store`, `Out io.Writer`, `Err io.Writer`, `Now func() time.Time`, `Cwd func() (string, error)` — injecting the clock and cwd is what makes every command testable.
  - `cli.NewRootCmd(app *App) *cobra.Command` — the whole command tree.
  - `cli.Rewrite(args []string, isCommand, isGroup func(string) bool) []string` — pure; the group-first sugar.
  - `cli.Execute() int` — builds real dependencies, returns a process exit code.
  - Commands: `init`, `add-repo`, `add-workflow`, `ls`.

`Rewrite` also passes through cobra's `__complete` prefix by rewriting the args *after* it, so the completion machinery in Task 17 sees canonical verb-first arguments and needs no special cases of its own.

- [ ] **Step 1: Write the failing rewrite test**

Create `internal/cli/rewrite_test.go`:

```go
package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testPredicates() (func(string) bool, func(string) bool) {
	commands := map[string]bool{
		"init": true, "add-repo": true, "add-workflow": true,
		"up": true, "ls": true, "var": true, "import": true,
		"shared": true, "completion": true, "help": true, "__complete": true,
	}
	groups := map[string]bool{"XYZ": true, "ABC": true}

	return func(s string) bool { return commands[s] }, func(s string) bool { return groups[s] }
}

func TestRewrite(t *testing.T) {
	isCommand, isGroup := testPredicates()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "no args", in: nil, want: nil},
		{name: "explicit verb untouched", in: []string{"up", "XYZ", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "registration command untouched", in: []string{"init", "NEW"}, want: []string{"init", "NEW"}},
		{name: "group then verb", in: []string{"XYZ", "up", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "group then workflow implies up", in: []string{"XYZ", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "group alone lists", in: []string{"XYZ"}, want: []string{"ls", "XYZ"}},
		{name: "group then verb with args", in: []string{"XYZ", "var", "backend", "A=1"}, want: []string{"var", "XYZ", "backend", "A=1"}},
		{name: "group then workflow with flags", in: []string{"XYZ", "local", "--dry-run"}, want: []string{"up", "XYZ", "local", "--dry-run"}},
		{name: "unknown first word untouched", in: []string{"typo", "local"}, want: []string{"typo", "local"}},
		{name: "leading flag untouched", in: []string{"--help"}, want: []string{"--help"}},
		{name: "leading short flag untouched", in: []string{"-h"}, want: []string{"-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rewrite(tt.in, isCommand, isGroup)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Rewrite(%v) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

// The shell calls `prizm __complete <words...>`; the words must be rewritten too
// or completion would have to duplicate the sugar rules.
func TestRewriteHandlesCompletionPrefix(t *testing.T) {
	isCommand, isGroup := testPredicates()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "completing a workflow", in: []string{"__complete", "XYZ", ""}, want: []string{"__complete", "up", "XYZ", ""}},
		{name: "partial workflow", in: []string{"__complete", "XYZ", "lo"}, want: []string{"__complete", "up", "XYZ", "lo"}},
		{name: "completing the first word", in: []string{"__complete", ""}, want: []string{"__complete", ""}},
		{name: "explicit verb", in: []string{"__complete", "up", "XYZ", ""}, want: []string{"__complete", "up", "XYZ", ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rewrite(tt.in, isCommand, isGroup)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Rewrite(%v) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/`
Expected: FAIL — `undefined: Rewrite`.

- [ ] **Step 3: Implement Rewrite**

Create `internal/cli/rewrite.go`:

```go
package cli

import "strings"

// completePrefix is the hidden command cobra's shell completion invokes.
const completePrefix = "__complete"

// Rewrite turns the group-first sugar into canonical verb-first arguments:
//
//	prizm XYZ up local  →  prizm up XYZ local
//	prizm XYZ local     →  prizm up XYZ local
//	prizm XYZ           →  prizm ls XYZ
//
// Anything that already starts with a command, a flag, or an unknown word is
// returned untouched so cobra can report the error itself.
func Rewrite(args []string, isCommand, isGroup func(string) bool) []string {
	if len(args) == 0 {
		return args
	}

	// Shell completion: rewrite the words the user actually typed.
	if args[0] == completePrefix {
		return append([]string{completePrefix}, Rewrite(args[1:], isCommand, isGroup)...)
	}

	head := args[0]
	if strings.HasPrefix(head, "-") || isCommand(head) || !isGroup(head) {
		return args
	}

	if len(args) == 1 {
		return []string{"ls", head}
	}

	if isCommand(args[1]) {
		out := []string{args[1], head}
		return append(out, args[2:]...)
	}

	out := []string{"up", head}
	return append(out, args[1:]...)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestRewrite -v`
Expected: PASS.

- [ ] **Step 5: Write the failing command test**

Create `internal/cli/commands_test.go`:

```go
package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

type harness struct {
	app *App
	out *bytes.Buffer
	err *bytes.Buffer
	cwd string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cwd := t.TempDir()
	h := &harness{out: &bytes.Buffer{}, err: &bytes.Buffer{}, cwd: cwd}
	h.app = &App{
		Store: s,
		Out:   h.out,
		Err:   h.err,
		Now:   func() time.Time { return time.Unix(1700000000, 0) },
		Cwd:   func() (string, error) { return h.cwd, nil },
	}
	return h
}

// run executes one command line through the real cobra tree.
func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()

	h.out.Reset()
	h.err.Reset()

	cmd := NewRootCmd(h.app)
	cmd.SetOut(h.out)
	cmd.SetErr(h.err)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestInitCreatesGroup(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "init", "XYZ"); err != nil {
		t.Fatalf("init error = %v", err)
	}
	if _, err := h.app.Store.GroupByName("XYZ"); err != nil {
		t.Fatalf("GroupByName() after init: %v", err)
	}
	if !strings.Contains(h.out.String(), "XYZ") {
		t.Errorf("output = %q, want it to mention the group", h.out.String())
	}
}

func TestInitRejectsDuplicateGroup(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "init", "XYZ"); err == nil {
		t.Fatal("second init error = nil, want error")
	}
}

func TestAddRepoDefaultsToCwd(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "backend"); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, err := h.app.Store.RepoByName(g.ID, "backend")
	if err != nil {
		t.Fatalf("RepoByName() error = %v", err)
	}
	if repo.Path != h.cwd {
		t.Errorf("Path = %q, want cwd %q", repo.Path, h.cwd)
	}
	if repo.EnvFile != ".env" {
		t.Errorf("EnvFile = %q, want %q", repo.EnvFile, ".env")
	}
}

func TestAddRepoStoresAbsolutePath(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "backend", "--path", "."); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	if !filepath.IsAbs(repo.Path) {
		t.Errorf("Path = %q, want an absolute path — paths are a stable contract", repo.Path)
	}
}

func TestAddRepoCustomEnvFile(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "frontend", "--env-file", ".env.local"); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "frontend")
	if repo.EnvFile != ".env.local" {
		t.Errorf("EnvFile = %q, want %q", repo.EnvFile, ".env.local")
	}
}

func TestAddRepoUnknownGroup(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "add-repo", "NOPE", "backend")
	if err == nil {
		t.Fatal("add-repo to unknown group error = nil, want error")
	}
	if !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("error = %q, want it to name the group", err)
	}
}

func TestAddWorkflowWithRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-repo", "XYZ", "ai")

	if err := h.run(t, "add-workflow", "XYZ", "local", "--repos", "frontend,backend", "--tag", "local"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, err := h.app.Store.WorkflowByName(g.ID, "local")
	if err != nil {
		t.Fatalf("WorkflowByName() error = %v", err)
	}
	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	if len(repos) != 2 {
		t.Errorf("workflow has %d repos, want 2", len(repos))
	}
	if wf.Tag != "local" {
		t.Errorf("Tag = %q, want %q", wf.Tag, "local")
	}
}

func TestAddWorkflowDefaultsToAllRepos(t *testing.T) {
	// The spec: defaulting to "all" is friendlier; explicit subsets are the
	// exception, not the rule.
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-repo", "XYZ", "backend")

	if err := h.run(t, "add-workflow", "XYZ", "full"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "full")
	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	if len(repos) != 2 {
		t.Errorf("workflow has %d repos, want all 2", len(repos))
	}
}

func TestAddWorkflowRejectsUnknownRepo(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")

	err := h.run(t, "add-workflow", "XYZ", "local", "--repos", "frontend,ghost")
	if err == nil {
		t.Fatal("error = nil, want error naming the unknown repo")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %q, want it to name %q", err, "ghost")
	}
}

func TestAddWorkflowRejectsReservedName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	err := h.run(t, "add-workflow", "XYZ", "status")
	if err == nil {
		t.Fatal("error = nil, want ErrReservedName")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("error = %q, want it to name the rejected word", err)
	}
}

func TestLsWithoutArgsListsGroups(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "init", "ABC")

	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("ls error = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "XYZ") || !strings.Contains(out, "ABC") {
		t.Errorf("ls output = %q, want both groups", out)
	}
}

func TestLsGroupListsWorkflowsAndRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--tag", "local")

	if err := h.run(t, "ls", "XYZ"); err != nil {
		t.Fatalf("ls XYZ error = %v", err)
	}
	out := h.out.String()
	for _, want := range []string{"backend", "local"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls XYZ output = %q, want it to mention %q", out, want)
		}
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestInit|TestAdd|TestLs'`
Expected: FAIL — `undefined: App`, `undefined: NewRootCmd`.

- [ ] **Step 7: Implement the root command**

Create `internal/cli/root.go`:

```go
// Package cli is prizm's command tree.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

// App carries everything the commands need. The clock and cwd are injected so
// every command is testable without touching the real environment.
type App struct {
	Store *store.Store
	Out   io.Writer
	Err   io.Writer
	Now   func() time.Time
	Cwd   func() (string, error)
}

// NewRootCmd builds the whole command tree.
func NewRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "prizm",
		Short: "Share env files across repos, grouped by workflow",
		Long: "prizm applies a named workflow's environment to every repo it covers,\n" +
			"building each repo's env file from shared and per-repo variables.",
		SilenceUsage:  true,
		SilenceErrors: false,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.SetOut(app.Out)
	root.SetErr(app.Err)

	root.AddCommand(
		newInitCmd(app),
		newAddRepoCmd(app),
		newAddWorkflowCmd(app),
		newLsCmd(app),
	)
	return root
}

// Execute wires real dependencies and returns a process exit code.
func Execute() int {
	key, err := crypto.LoadOrCreateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "prizm:", err)
		return 1
	}
	cipher, err := crypto.NewAESGCM(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prizm:", err)
		return 1
	}

	dbPath, err := config.DBPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "prizm:", err)
		return 1
	}
	if err := config.EnsureDir(filepath.Dir(dbPath)); err != nil {
		fmt.Fprintln(os.Stderr, "prizm:", err)
		return 1
	}

	s, err := store.Open(dbPath, cipher)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prizm:", err)
		return 1
	}
	defer s.Close()

	app := &App{Store: s, Out: os.Stdout, Err: os.Stderr, Now: time.Now, Cwd: os.Getwd}
	root := NewRootCmd(app)
	root.SetArgs(rewriteArgs(app, root, os.Args[1:]))

	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}

// rewriteArgs applies the group-first sugar using the live command tree and DB.
func rewriteArgs(app *App, root *cobra.Command, args []string) []string {
	isCommand := func(name string) bool {
		if name == completePrefix || name == "help" || name == "completion" {
			return true
		}
		for _, c := range root.Commands() {
			if c.Name() == name || c.HasAlias(name) {
				return true
			}
		}
		return false
	}
	isGroup := func(name string) bool {
		_, err := app.Store.GroupByName(name)
		return err == nil
	}
	return Rewrite(args, isCommand, isGroup)
}

// mustGroup resolves a group name or returns a user-facing error.
func (a *App) mustGroup(name string) (store.Group, error) {
	g, err := a.Store.GroupByName(name)
	if err != nil {
		return store.Group{}, fmt.Errorf("no such group %q — run `prizm init %s` first", name, name)
	}
	return g, nil
}
```

- [ ] **Step 8: Implement the registration commands**

Create `internal/cli/group.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newInitCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "init <group>",
		Short: "Create a new group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.Store.CreateGroup(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "created group %s\n", g.Name)
			return nil
		},
	}
}

func newLsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "ls [group]",
		Short:   "List groups, or one group's repos and workflows",
		Aliases: []string{"list"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listGroups(app)
			}
			return listGroup(app, args[0])
		},
	}
}

func listGroups(app *App) error {
	groups, err := app.Store.ListGroups()
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Fprintln(app.Out, "no groups yet — run `prizm init <name>`")
		return nil
	}
	for _, g := range groups {
		fmt.Fprintln(app.Out, g.Name)
	}
	return nil
}

func listGroup(app *App, name string) error {
	g, err := app.mustGroup(name)
	if err != nil {
		return err
	}

	repos, err := app.Store.ListRepos(g.ID)
	if err != nil {
		return err
	}
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return err
	}

	fmt.Fprintf(app.Out, "%s\n", g.Name)

	fmt.Fprintln(app.Out, "  repos:")
	for _, r := range repos {
		fmt.Fprintf(app.Out, "    %-16s %s\n", r.Name, r.Path)
	}

	fmt.Fprintln(app.Out, "  workflows:")
	for _, w := range workflows {
		tag := ""
		if w.Tag != "" {
			tag = "  [" + w.Tag + "]"
		}
		members, err := app.Store.WorkflowRepos(w.ID)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Name)
		}
		fmt.Fprintf(app.Out, "    %-16s %v%s\n", w.Name, names, tag)
	}
	return nil
}
```

Create `internal/cli/repo.go`:

```go
package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newAddRepoCmd(app *App) *cobra.Command {
	var (
		path    string
		envFile string
	)

	cmd := &cobra.Command{
		Use:   "add-repo <group> <repo>",
		Short: "Register a repo in a group (defaults to the current directory)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}

			abs, err := resolvePath(app, path)
			if err != nil {
				return err
			}

			repo, err := app.Store.AddRepo(g.ID, args[1], abs, envFile)
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "added %s/%s → %s (%s)\n", g.Name, repo.Name, repo.Path, repo.EnvFile)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "repo path (default: current directory)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "env file name to link inside the repo (default: .env)")
	return cmd
}

// resolvePath turns a possibly-relative path into an absolute one. Repo paths
// are a stable contract, so they are always stored absolute.
func resolvePath(app *App, path string) (string, error) {
	if path == "" {
		cwd, err := app.Cwd()
		if err != nil {
			return "", fmt.Errorf("determining current directory: %w", err)
		}
		return cwd, nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	cwd, err := app.Cwd()
	if err != nil {
		return "", fmt.Errorf("determining current directory: %w", err)
	}
	return filepath.Join(cwd, path), nil
}
```

Create `internal/cli/workflow.go`:

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
)

func newAddWorkflowCmd(app *App) *cobra.Command {
	var (
		repoList string
		tag      string
	)

	cmd := &cobra.Command{
		Use:   "add-workflow <group> <workflow>",
		Short: "Define a workflow: a named bundle of repos",
		Long: "Without --repos the workflow covers every repo currently in the group.\n" +
			"Pass --repos to cover an explicit subset, e.g. a frontend-only workflow.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}

			repoIDs, err := resolveRepoIDs(app, g, repoList)
			if err != nil {
				return err
			}

			wf, err := app.Store.AddWorkflow(g.ID, args[1], tag, repoIDs)
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "added workflow %s/%s covering %d repo(s)\n", g.Name, wf.Name, len(repoIDs))
			return nil
		},
	}

	cmd.Flags().StringVar(&repoList, "repos", "", "comma-separated repo names (default: every repo in the group)")
	cmd.Flags().StringVar(&tag, "tag", "", "guardrail tag, e.g. prod/qa/local")
	return cmd
}

// resolveRepoIDs turns a comma-separated repo list into IDs, defaulting to
// every repo in the group when the list is empty.
func resolveRepoIDs(app *App, g store.Group, list string) ([]int64, error) {
	if strings.TrimSpace(list) == "" {
		repos, err := app.Store.ListRepos(g.ID)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(repos))
		for _, r := range repos {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}

	var ids []int64
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		repo, err := app.Store.RepoByName(g.ID, name)
		if err != nil {
			return nil, fmt.Errorf("no such repo %q in group %s", name, g.Name)
		}
		ids = append(ids, repo.ID)
	}
	return ids, nil
}
```

- [ ] **Step 9: Run test to verify it passes**

Run: `go get github.com/spf13/cobra@latest && go test ./internal/cli/ -v`
Expected: PASS — rewrite and command tests.

- [ ] **Step 10: Wire up main**

Replace `main.go`:

```go
package main

import (
	"os"

	"github.com/troglodytto/prizm/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
```

Run: `go build -o /tmp/prizm . && /tmp/prizm --help`
Expected: the help text lists `init`, `add-repo`, `add-workflow`, `ls`.

- [ ] **Step 11: Commit**

```bash
git add go.mod go.sum main.go internal/cli/
git commit -m "feat(cli): root command, group-first argument sugar, registration commands"
```

---

### Task 14: CLI — setting and importing variables

**Files:**
- Create: `internal/cli/vars.go`
- Modify: `internal/cli/root.go` (register the two new commands)
- Test: `internal/cli/vars_test.go`

**Interfaces:**
- Consumes: `App` and `mustGroup` (Task 13); `store.SetRepoVar`, `SetWorkflowRepoVar`, `RepoByName`, `WorkflowByName` (Tasks 6–8); `envfile.Parse` (Task 2).
- Produces:
  - `prizm var <group> <repo> KEY=VALUE [KEY=VALUE...]` — writes the repo-shared layer; `--workflow <name>` writes the repo+workflow layer instead.
  - `prizm import <group> <repo> <file>` — bulk-loads an existing `.env`; same `--workflow` flag.
  - `cli.parseAssignment(arg string) (key, value string, err error)`

`import` matters more than it looks: the spec calls it the on-ramp, because everyone already has `.env.local` files sitting in their repos and that is how prizm gets populated the first time.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/vars_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseAssignment(t *testing.T) {
	tests := []struct {
		in        string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{in: "A=1", wantKey: "A", wantValue: "1"},
		{in: "DSN=postgres://u:p@h/db?a=b", wantKey: "DSN", wantValue: "postgres://u:p@h/db?a=b"},
		{in: "EMPTY=", wantKey: "EMPTY", wantValue: ""},
		{in: "_PRIZM_PASS=hunter2", wantKey: "_PRIZM_PASS", wantValue: "hunter2"},
		{in: "TEMPLATE=${_PRIZM_DB_URL}", wantKey: "TEMPLATE", wantValue: "${_PRIZM_DB_URL}"},
		{in: "NOEQUALS", wantErr: true},
		{in: "=novalue", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			key, value, err := parseAssignment(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAssignment(%q) error = nil, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAssignment(%q) error = %v", tt.in, err)
			}
			if key != tt.wantKey || value != tt.wantValue {
				t.Errorf("parseAssignment(%q) = (%q, %q), want (%q, %q)", tt.in, key, value, tt.wantKey, tt.wantValue)
			}
		})
	}
}

func (h *harness) seedGroup(t *testing.T) {
	t.Helper()

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-workflow", "XYZ", "local")
}

func TestVarSetsRepoSharedLayer(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "LOG_LEVEL=debug"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if diff := cmp.Diff(map[string]string{"LOG_LEVEL": "debug"}, got); diff != "" {
		t.Errorf("RepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestVarSetsWorkflowLayerWithFlag(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if diff := cmp.Diff(map[string]string{"PORT": "8080"}, got); diff != "" {
		t.Errorf("WorkflowRepoVars() mismatch (-want +got):\n%s", diff)
	}
	if shared, _ := h.app.Store.RepoVars(repo.ID); len(shared) != 0 {
		t.Errorf("repo-shared layer = %v, want empty — --workflow must scope the write", shared)
	}
}

func TestVarSetsSeveralAssignmentsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "A=1", "B=2", "C=3"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if len(got) != 3 {
		t.Errorf("RepoVars() = %v, want 3 entries", got)
	}
}

func TestVarStoresTemplateVerbatim(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if got["DB_URL"] != "${_PRIZM_DB_URL}" {
		t.Errorf("DB_URL = %q, want the literal template", got["DB_URL"])
	}
}

func TestVarRejectsUnknownRepo(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	err := h.run(t, "var", "XYZ", "ghost", "A=1")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown repo", err)
	}
}

func TestVarRejectsUnknownWorkflow(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	err := h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown workflow", err)
	}
}

func TestImportLoadsAnEnvFile(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	path := filepath.Join(t.TempDir(), ".env.local")
	body := "# a comment\nexport PORT=8080\nDSN=\"postgres://u:p@h/db\"\n\nDEBUG=true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if err := h.run(t, "import", "XYZ", "backend", path, "--workflow", "local"); err != nil {
		t.Fatalf("import error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	want := map[string]string{"PORT": "8080", "DSN": "postgres://u:p@h/db", "DEBUG": "true"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("imported vars mismatch (-want +got):\n%s", diff)
	}
}

func TestImportReportsHowManyVarsItLoaded(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600)

	if err := h.run(t, "import", "XYZ", "backend", path); err != nil {
		t.Fatalf("import error = %v", err)
	}
	if !strings.Contains(h.out.String(), "2") {
		t.Errorf("output = %q, want it to report the count", h.out.String())
	}
}

func TestImportMissingFile(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	err := h.run(t, "import", "XYZ", "backend", filepath.Join(t.TempDir(), "nope.env"))
	if err == nil {
		t.Fatal("import of a missing file error = nil, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestVar|TestImport|TestParseAssignment'`
Expected: FAIL — `undefined: parseAssignment`, unknown command `var`.

- [ ] **Step 3: Implement the commands**

Create `internal/cli/vars.go`:

```go
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/store"
)

func newVarCmd(app *App) *cobra.Command {
	var workflow string

	cmd := &cobra.Command{
		Use:   "var <group> <repo> KEY=VALUE [KEY=VALUE...]",
		Short: "Set variables on a repo",
		Long: "Without --workflow the variables apply in every workflow that touches\n" +
			"this repo. With --workflow they apply only there, and win over both the\n" +
			"repo-shared layer and any shared bag.\n\n" +
			"Values are stored verbatim: ${OTHER_VAR} references are expanded at `up`\n" +
			"time, not here. Keys starting with _PRIZM_ are internal — usable in\n" +
			"templates, never written to the repo's env file.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repo, wf, err := resolveTarget(app, args[0], args[1], workflow)
			if err != nil {
				return err
			}

			for _, assignment := range args[2:] {
				key, value, err := parseAssignment(assignment)
				if err != nil {
					return err
				}

				if workflow == "" {
					err = app.Store.SetRepoVar(repo.ID, key, value)
				} else {
					err = app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, key, value)
				}
				if err != nil {
					return err
				}
			}

			fmt.Fprintf(app.Out, "set %d variable(s) on %s/%s%s\n",
				len(args)-2, g.Name, repo.Name, scopeSuffix(workflow))
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "scope the variables to one workflow")
	return cmd
}

func newImportCmd(app *App) *cobra.Command {
	var workflow string

	cmd := &cobra.Command{
		Use:   "import <group> <repo> <file>",
		Short: "Load an existing .env file into prizm",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repo, wf, err := resolveTarget(app, args[0], args[1], workflow)
			if err != nil {
				return err
			}

			raw, err := os.ReadFile(args[2])
			if err != nil {
				return fmt.Errorf("reading %s: %w", args[2], err)
			}
			vars, err := envfile.Parse(string(raw))
			if err != nil {
				return fmt.Errorf("parsing %s: %w", args[2], err)
			}

			for key, value := range vars {
				if workflow == "" {
					err = app.Store.SetRepoVar(repo.ID, key, value)
				} else {
					err = app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, key, value)
				}
				if err != nil {
					return err
				}
			}

			fmt.Fprintf(app.Out, "imported %d variable(s) into %s/%s%s\n",
				len(vars), g.Name, repo.Name, scopeSuffix(workflow))
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "scope the imported variables to one workflow")
	return cmd
}

// resolveTarget looks up the group, repo and (optionally) workflow a write targets.
func resolveTarget(app *App, groupName, repoName, workflowName string) (store.Group, store.Repo, store.Workflow, error) {
	g, err := app.mustGroup(groupName)
	if err != nil {
		return store.Group{}, store.Repo{}, store.Workflow{}, err
	}

	repo, err := app.Store.RepoByName(g.ID, repoName)
	if err != nil {
		return store.Group{}, store.Repo{}, store.Workflow{},
			fmt.Errorf("no such repo %q in group %s", repoName, g.Name)
	}

	var wf store.Workflow
	if workflowName != "" {
		wf, err = app.Store.WorkflowByName(g.ID, workflowName)
		if err != nil {
			return store.Group{}, store.Repo{}, store.Workflow{},
				fmt.Errorf("no such workflow %q in group %s", workflowName, g.Name)
		}
	}
	return g, repo, wf, nil
}

// parseAssignment splits KEY=VALUE. Only the first '=' separates.
func parseAssignment(arg string) (string, string, error) {
	key, value, found := strings.Cut(arg, "=")
	if !found {
		return "", "", fmt.Errorf("%q is not a KEY=VALUE assignment", arg)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("%q has an empty key", arg)
	}
	return key, value, nil
}

func scopeSuffix(workflow string) string {
	if workflow == "" {
		return " (all workflows)"
	}
	return " (" + workflow + ")"
}
```

- [ ] **Step 4: Register the commands**

In `internal/cli/root.go`, extend the `root.AddCommand(...)` call:

```go
	root.AddCommand(
		newInitCmd(app),
		newAddRepoCmd(app),
		newAddWorkflowCmd(app),
		newLsCmd(app),
		newVarCmd(app),
		newImportCmd(app),
	)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): var and import commands across all three layers"
```

---

### Task 15: CLI — `up`

**Files:**
- Create: `internal/cli/up.go`
- Modify: `internal/cli/root.go` (register `up`, add `Confirm` to `App`)
- Test: `internal/cli/up_test.go`

**Interfaces:**
- Consumes: `resolve.ForRepo`, `Expand`, `Emit`, `ErrUnresolved`, `ErrCycle` (Task 9); `envfile.Render` (Task 2); `apply.Apply` (Task 11); `config.BuiltPath` (Task 1); `store.WorkflowRepos`, `TouchGroup` (Tasks 5, 7).
- Produces:
  - `App.Confirm func(prompt string) (bool, error)` — new field, injected so the prod guardrail is testable.
  - `store.(*Store).RecordApplied(repoID, workflowID int64, builtPath string, now time.Time) error` — added to `internal/store/store.go`.
  - `prizm up <group> <workflow>` with `--yes` to skip the prod-tag confirmation.

Behaviour, straight from the spec:

- Repos are processed in name order; each is independent. A repo that fails (unresolved reference, cycle, missing path) is reported and **skipped**, leaving its existing env file untouched, while every other repo still applies. `up` exits non-zero if any repo failed.
- A workflow tagged `prod` asks for confirmation first unless `--yes` was passed.
- `up` never prompts about divergence and never blocks on it — that is `sync`'s job in a later phase.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/up_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoDir makes a real directory for a repo to be linked into.
func (h *harness) repoDir(t *testing.T, name string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func TestUpWritesAndLinksEachRepo(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	feDir := h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")
	h.run(t, "var", "XYZ", "frontend", "API_URL=http://localhost:8080", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v\nstderr: %s", err, h.err.String())
	}

	for dir, want := range map[string]string{
		beDir: "PORT=8080\n",
		feDir: "API_URL=http://localhost:8080\n",
	} {
		link := filepath.Join(dir, ".env")
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("lstat %s: %v", link, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", link)
		}
		got, err := os.ReadFile(link)
		if err != nil {
			t.Fatalf("reading %s: %v", link, err)
		}
		if string(got) != want {
			t.Errorf("%s content = %q, want %q", link, got, want)
		}
	}
}

// The end-to-end shape the whole design exists for.
func TestUpResolvesSharedDerivedValuesOpaquely(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	authDir := h.repoDir(t, "auth")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "auth", "--path", authDir)
	h.run(t, "add-workflow", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	be, _ := h.app.Store.RepoByName(g.ID, "backend")
	auth, _ := h.app.Store.RepoByName(g.ID, "auth")

	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.AddSharedGroupRepo(sg.ID, be.ID)
	h.app.Store.AddSharedGroupRepo(sg.ID, auth.ID)
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "var", "XYZ", "auth", "DATABASE_URL=${_PRIZM_DB_URL}", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v\nstderr: %s", err, h.err.String())
	}

	dsn := "postgres://svc_app:hunter2@localhost:5432/app"
	for dir, want := range map[string]string{
		beDir:   "DB_URL=" + dsn + "\n",
		authDir: "DATABASE_URL=" + dsn + "\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatalf("reading env: %v", err)
		}
		if string(got) != want {
			t.Errorf("%s/.env = %q, want %q", dir, got, want)
		}
		// The file must carry no trace of the derivation.
		if strings.Contains(string(got), "_PRIZM_") {
			t.Errorf("%s/.env leaked internal plumbing: %q", dir, got)
		}
	}
}

func TestUpSkipsFailingRepoAndContinues(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	feDir := h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	// backend references something that does not exist.
	h.run(t, "var", "XYZ", "backend", "DB_URL=postgres://${_PRIZM_MISSING}@h/db", "--workflow", "local")
	h.run(t, "var", "XYZ", "frontend", "API_URL=http://localhost", "--workflow", "local")

	err := h.run(t, "up", "XYZ", "local")
	if err == nil {
		t.Fatal("up error = nil, want a non-zero result when a repo failed")
	}

	// frontend still applied.
	if _, statErr := os.Lstat(filepath.Join(feDir, ".env")); statErr != nil {
		t.Errorf("frontend was not applied: %v", statErr)
	}
	// backend was left alone.
	if _, statErr := os.Lstat(filepath.Join(beDir, ".env")); !os.IsNotExist(statErr) {
		t.Errorf("backend .env exists; a failing repo must be left untouched")
	}

	out := h.out.String() + h.err.String()
	if !strings.Contains(out, "_PRIZM_MISSING") || !strings.Contains(out, "backend") {
		t.Errorf("output = %q, want it to name the repo and the missing reference", out)
	}
}

func TestUpPreservesAnExistingRealEnvFile(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	existing := filepath.Join(beDir, ".env")
	os.WriteFile(existing, []byte("PRECIOUS=keepme\n"), 0o600)

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v", err)
	}

	entries, _ := os.ReadDir(beDir)
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), ".prizm-backup.") {
			found = true
			body, _ := os.ReadFile(filepath.Join(beDir, e.Name()))
			if string(body) != "PRECIOUS=keepme\n" {
				t.Errorf("backup content = %q, want the original", body)
			}
		}
	}
	if !found {
		t.Error("no .prizm-backup file; the user's original .env was destroyed")
	}
}

func TestUpPromptsForProdTaggedWorkflow(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	var prompted string
	h.app.Confirm = func(prompt string) (bool, error) {
		prompted = prompt
		return false, nil // decline
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "production")

	if err := h.run(t, "up", "XYZ", "production"); err == nil {
		t.Fatal("up error = nil, want an aborted run after declining")
	}
	if !strings.Contains(prompted, "production") {
		t.Errorf("prompt = %q, want it to name the workflow", prompted)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error("declining the prompt still applied the workflow")
	}
}

func TestUpProdPromptSkippedWithYes(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called despite --yes")
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "production")

	if err := h.run(t, "up", "XYZ", "production", "--yes"); err != nil {
		t.Fatalf("up --yes error = %v", err)
	}
}

func TestUpDoesNotPromptForUntaggedWorkflow(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called for an untagged workflow")
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v", err)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	for i := 0; i < 3; i++ {
		if err := h.run(t, "up", "XYZ", "local"); err != nil {
			t.Fatalf("up run %d error = %v", i, err)
		}
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("repo dir has %v, want only .env — repeated ups must not accumulate files", names)
	}
}

func TestUpRecordsWhatWasApplied(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")

	// Frecency must advance so completion can rank this group.
	g, _ := h.app.Store.GroupByName("XYZ")
	if g.UseCount != 1 {
		t.Errorf("UseCount = %d, want 1 after one up", g.UseCount)
	}
}

func TestUpUnknownWorkflow(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	err := h.run(t, "up", "XYZ", "ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown workflow", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestUp`
Expected: FAIL — unknown command `up`.

- [ ] **Step 3: Add the applied-state recorder to the store**

Append to `internal/store/store.go`:

```go
// RecordApplied notes which workflow a repo is currently linked to. This is
// what `prizm status` reads in a later phase.
func (s *Store) RecordApplied(repoID, workflowID int64, builtPath string, now time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO applied(repo_id, workflow_id, built_path, applied_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET
			workflow_id = excluded.workflow_id,
			built_path  = excluded.built_path,
			applied_at  = excluded.applied_at`,
		repoID, workflowID, builtPath, now.Unix())
	return err
}
```

Add `"time"` to that file's imports.

- [ ] **Step 4: Implement `up`**

Create `internal/cli/up.go`:

```go
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/apply"
	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
)

// prodTag is the guardrail tag that triggers a confirmation prompt.
const prodTag = "prod"

func newUpCmd(app *App) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "up <group> <workflow>",
		Short: "Apply a workflow: build and link every covered repo's env file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}
			wf, err := app.Store.WorkflowByName(g.ID, args[1])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", args[1], g.Name)
			}

			if wf.Tag == prodTag && !yes {
				ok, err := app.Confirm(fmt.Sprintf(
					"%s/%s is tagged %s. Apply it? [y/N] ", g.Name, wf.Name, prodTag))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}

			repos, err := app.Store.WorkflowRepos(wf.ID)
			if err != nil {
				return err
			}
			if len(repos) == 0 {
				fmt.Fprintf(app.Out, "workflow %s covers no repos\n", wf.Name)
				return nil
			}

			failed := 0
			for _, repo := range repos {
				if err := applyRepo(app, g, wf, repo); err != nil {
					failed++
					fmt.Fprintln(app.Out, style.Row(style.Fail, repo.Name, err.Error()))
					continue
				}
				fmt.Fprintln(app.Out, style.Row(style.OK, repo.Name, "set ("+wf.Name+")"))
			}

			if err := app.Store.TouchGroup(g.ID, app.Now()); err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d repo(s) failed", failed, len(repos))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for prod-tagged workflows")
	return cmd
}

// applyRepo resolves, expands and writes one repo's env file. Any failure
// leaves that repo's existing env file exactly as it was.
func applyRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) error {
	templates, err := resolve.ForRepo(app.Store, wf, repo)
	if err != nil {
		return err
	}

	expanded, err := resolve.Expand(templates)
	if err != nil {
		// Unresolved references and cycles are user errors; say so plainly.
		if errors.Is(err, resolve.ErrUnresolved) || errors.Is(err, resolve.ErrCycle) {
			return fmt.Errorf("%v — %s left unchanged", err, repo.EnvFile)
		}
		return err
	}

	content := envfile.Render(resolve.Emit(expanded))

	builtPath, err := config.BuiltPath(g.Name, wf.Name, repo.Name)
	if err != nil {
		return err
	}

	res, err := apply.Apply(builtPath, content, repo.Path, repo.EnvFile, app.Now())
	if err != nil {
		return err
	}
	if res.BackedUpTo != "" {
		fmt.Fprintln(app.Out, style.Detail("  backed up existing "+repo.EnvFile+" → "+res.BackedUpTo))
	}

	return app.Store.RecordApplied(repo.ID, wf.ID, res.BuiltPath, app.Now())
}
```

- [ ] **Step 5: Add `Confirm` to App and register `up`**

In `internal/cli/root.go`, add the field to `App`:

```go
type App struct {
	Store   *store.Store
	Out     io.Writer
	Err     io.Writer
	Now     func() time.Time
	Cwd     func() (string, error)
	Confirm func(prompt string) (bool, error)
}
```

Add the default implementation to the same file:

```go
// confirmOnStdin is the real prompt. Anything other than y/yes declines.
func confirmOnStdin(out io.Writer) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		fmt.Fprint(out, prompt)

		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, nil
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}
```

Add `"bufio"` and `"strings"` to that file's imports, set the field in `Execute`:

```go
	app := &App{Store: s, Out: os.Stdout, Err: os.Stderr, Now: time.Now, Cwd: os.Getwd}
	app.Confirm = confirmOnStdin(app.Out)
```

and register the command in `root.AddCommand(...)`:

```go
		newUpCmd(app),
```

Finally, give the test harness a default in `newHarness` (`internal/cli/commands_test.go`), added to the `App` literal:

```go
		Confirm: func(string) (bool, error) { return true, nil },
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS — all `up` tests plus everything earlier.

- [ ] **Step 7: Try it for real**

```bash
go build -o /tmp/prizm .
export XDG_DATA_HOME=$(mktemp -d)
mkdir -p /tmp/demo/backend /tmp/demo/frontend
/tmp/prizm init DEMO
/tmp/prizm add-repo DEMO backend --path /tmp/demo/backend
/tmp/prizm add-repo DEMO frontend --path /tmp/demo/frontend
/tmp/prizm add-workflow DEMO local
/tmp/prizm var DEMO backend PORT=8080 --workflow local
/tmp/prizm DEMO local          # the sugar form
cat /tmp/demo/backend/.env
```

Expected: `✓ backend set (local)`, `✓ frontend set (local)`, and `PORT=8080` in the file. Note this run uses your real OS keychain for the first time.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/ internal/store/
git commit -m "feat(cli): up — resolve, expand, render and link every repo in a workflow"
```

---

### Task 16: Run from anywhere — infer the group (and repo) from the current directory

**Files:**
- Create: `internal/store/locate.go`, `internal/cli/locate.go`
- Modify: `internal/cli/rewrite.go` (the `Rewrite` signature), `internal/cli/rewrite_test.go` (call sites), `internal/cli/root.go` (`rewriteArgs`), `internal/cli/up.go`, `internal/cli/vars.go` (argument counts)
- Test: `internal/store/locate_test.go`, `internal/cli/locate_test.go`

**Interfaces:**
- Consumes: `store.Repo`, `store.Group` (Tasks 5–6); `App.Cwd` (Task 13).
- Produces:
  - `store.(*Store).RepoForPath(path string) (Repo, Group, error)` — the registered repo whose path contains `path`, longest match wins; `ErrNotFound` when standing outside every repo.
  - `cli.Resolver` struct: `IsCommand func(string) bool`, `IsGroup func(string) bool`, `InferGroup func() (string, bool)`, `IsWorkflow func(group, name string) bool`.
  - `cli.Rewrite(args []string, r Resolver) []string` — **replaces** the three-argument form from Task 13.
  - `cli.(*App).splitGroup(args []string, want int) (store.Group, []string, error)`
  - `cli.(*App).splitGroupRepo(args []string, want int) (store.Group, store.Repo, []string, error)`

The rule, in one line: **positional count decides.** A command needing `want` positionals after the group treats `want+1` arguments as "group given explicitly" and `want` as "infer it from where I'm standing". Same for the group+repo pair. Nothing is guessed when it was stated.

```bash
cd ~/code/xyz/backend
prizm up local            # group inferred from cwd
prizm local               # …and the verb inferred too
prizm var PORT=8080 --workflow local     # group and repo both inferred
cd ~
prizm up XYZ local        # outside every repo: state the group
prizm up local            # error — see below
```

**The ambiguous case is an error, deliberately.** Run from outside every registered repo with no group name, prizm cannot know which group you meant, and picking one would be a guess that silently writes files into repos. It fails with a message that names the fix.

> **Noted for later, not built now.** Three ways this could resolve instead of erroring, in increasing order of how much they assume: (1) if exactly one group exists, use it — safe and trivial, but stops working the moment a second group appears, which is a nasty cliff; (2) fall back to the most recent group by the frecency data `TouchGroup` already records, requiring confirmation before it writes anything; (3) open the Bubble Tea picker, which the spec already wants for a bare `prizm` — this is the real answer, and it is why the ambiguous case should stay an error until the TUI phase rather than acquiring a heuristic that the picker would then have to unlearn.

- [ ] **Step 1: Write the failing store test**

Create `internal/store/locate_test.go`:

```go
package store

import (
	"errors"
	"testing"
)

func TestRepoForPathFindsContainingRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")
	s.AddRepo(g.ID, "frontend", "/code/xyz/frontend", "")

	repo, group, err := s.RepoForPath("/code/xyz/backend/src/handlers")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "backend" {
		t.Errorf("repo = %q, want %q", repo.Name, "backend")
	}
	if group.Name != "XYZ" {
		t.Errorf("group = %q, want %q", group.Name, "XYZ")
	}
}

func TestRepoForPathExactMatch(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	repo, _, err := s.RepoForPath("/code/xyz/backend")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "backend" {
		t.Errorf("repo = %q, want %q", repo.Name, "backend")
	}
}

func TestRepoForPathPrefersLongestMatch(t *testing.T) {
	// A repo checked out inside another repo must win over its parent.
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "monorepo", "/code/xyz", "")
	s.AddRepo(g.ID, "nested", "/code/xyz/packages/api", "")

	repo, _, err := s.RepoForPath("/code/xyz/packages/api/src")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "nested" {
		t.Errorf("repo = %q, want %q — the deeper repo should win", repo.Name, "nested")
	}
}

func TestRepoForPathRespectsPathBoundaries(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	// /code/xyz/backend-old is NOT inside /code/xyz/backend.
	if _, _, err := s.RepoForPath("/code/xyz/backend-old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}

func TestRepoForPathOutsideEveryRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	if _, _, err := s.RepoForPath("/somewhere/else"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}

func TestRepoForPathWithNoRepos(t *testing.T) {
	s := newTestStore(t)

	if _, _, err := s.RepoForPath("/anywhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRepoForPath`
Expected: FAIL — `s.RepoForPath undefined`.

- [ ] **Step 3: Implement RepoForPath**

Create `internal/store/locate.go`:

```go
package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RepoForPath returns the registered repo containing path, along with its
// group. When repos are nested, the deepest one wins. Returns ErrNotFound
// when path is outside every registered repo.
func (s *Store) RepoForPath(path string) (Repo, Group, error) {
	path = filepath.Clean(path)

	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file,
		       g.id, g.name, g.use_count, g.last_used_at
		FROM repos r
		JOIN "groups" g ON g.id = r.group_id`)
	if err != nil {
		return Repo{}, Group{}, err
	}
	defer rows.Close()

	var (
		bestRepo  Repo
		bestGroup Group
		bestLen   int
	)
	for rows.Next() {
		var (
			r        Repo
			g        Group
			lastUsed int64
		)
		if err := rows.Scan(
			&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile,
			&g.ID, &g.Name, &g.UseCount, &lastUsed,
		); err != nil {
			return Repo{}, Group{}, err
		}

		clean := filepath.Clean(r.Path)
		if !pathContains(clean, path) {
			continue
		}
		if len(clean) > bestLen {
			bestLen, bestRepo, bestGroup = len(clean), r, g
		}
	}
	if err := rows.Err(); err != nil {
		return Repo{}, Group{}, err
	}

	if bestLen == 0 {
		return Repo{}, Group{}, fmt.Errorf("no registered repo contains %s: %w", path, ErrNotFound)
	}
	return bestRepo, bestGroup, nil
}

// pathContains compares whole path segments, so /code/x does not contain
// /code/x-old.
func pathContains(dir, child string) bool {
	return child == dir || strings.HasPrefix(child, dir+string(filepath.Separator))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestRepoForPath -v`
Expected: PASS — all six tests.

- [ ] **Step 5: Write the failing CLI resolution test**

Create `internal/cli/locate_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

// seedLocated registers two repos with real directories and returns them.
func (h *harness) seedLocated(t *testing.T) (beDir, feDir string) {
	t.Helper()

	beDir = h.repoDir(t, "backend")
	feDir = h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	return beDir, feDir
}

func TestSplitGroupUsesExplicitName(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	g, rest, err := h.app.splitGroup([]string{"XYZ", "local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q", g.Name, "XYZ")
	}
	if len(rest) != 1 || rest[0] != "local" {
		t.Errorf("rest = %v, want [local]", rest)
	}
}

func TestSplitGroupInfersFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, rest, err := h.app.splitGroup([]string{"local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q inferred from cwd", g.Name, "XYZ")
	}
	if len(rest) != 1 || rest[0] != "local" {
		t.Errorf("rest = %v, want [local]", rest)
	}
}

func TestSplitGroupInfersFromASubdirectory(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir + "/src/handlers"

	g, _, err := h.app.splitGroup([]string{"local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q", g.Name, "XYZ")
	}
}

func TestSplitGroupAmbiguousOutsideAnyRepo(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	_, _, err := h.app.splitGroup([]string{"local"}, 1)
	if err == nil {
		t.Fatal("splitGroup() error = nil, want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Errorf("error = %q, want it to tell the user to name the group", err)
	}
}

func TestSplitGroupRepoInfersBoth(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, repo, rest, err := h.app.splitGroupRepo([]string{"PORT=8080"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "backend" {
		t.Errorf("got %s/%s, want XYZ/backend", g.Name, repo.Name)
	}
	if len(rest) != 1 || rest[0] != "PORT=8080" {
		t.Errorf("rest = %v, want [PORT=8080]", rest)
	}
}

func TestSplitGroupRepoInfersGroupOnly(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, repo, _, err := h.app.splitGroupRepo([]string{"frontend", "PORT=3000"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "frontend" {
		t.Errorf("got %s/%s, want XYZ/frontend — an explicit repo overrides cwd", g.Name, repo.Name)
	}
}

func TestSplitGroupRepoFullyExplicit(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	g, repo, _, err := h.app.splitGroupRepo([]string{"XYZ", "backend", "PORT=8080"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "backend" {
		t.Errorf("got %s/%s, want XYZ/backend", g.Name, repo.Name)
	}
}

// End-to-end: the command itself, run from inside a repo, with no group.
func TestUpInfersGroupFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")
	h.cwd = beDir

	if err := h.run(t, "up", "local"); err != nil {
		t.Fatalf("up local error = %v\nstderr: %s", err, h.err.String())
	}
	if !strings.Contains(h.out.String(), "backend") {
		t.Errorf("output = %q, want it to have applied the inferred group", h.out.String())
	}
}

func TestVarInfersGroupAndRepoFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	if err := h.run(t, "var", "LOG_LEVEL=debug"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if got["LOG_LEVEL"] != "debug" {
		t.Errorf("RepoVars() = %v, want LOG_LEVEL set on the inferred repo", got)
	}
}

// The bare-workflow sugar, from inside a repo: `prizm local`.
func TestRewriteInfersGroupForBareWorkflow(t *testing.T) {
	r := Resolver{
		IsCommand:  func(s string) bool { return s == "up" || s == "ls" },
		IsGroup:    func(s string) bool { return s == "XYZ" },
		InferGroup: func() (string, bool) { return "XYZ", true },
		IsWorkflow: func(group, name string) bool { return group == "XYZ" && name == "local" },
	}

	got := Rewrite([]string{"local"}, r)
	want := []string{"up", "XYZ", "local"}
	if len(got) != len(want) {
		t.Fatalf("Rewrite() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rewrite() = %v, want %v", got, want)
		}
	}
}

func TestRewriteLeavesUnknownWordAloneWhenNotAWorkflow(t *testing.T) {
	r := Resolver{
		IsCommand:  func(s string) bool { return s == "up" },
		IsGroup:    func(string) bool { return false },
		InferGroup: func() (string, bool) { return "XYZ", true },
		IsWorkflow: func(string, string) bool { return false },
	}

	got := Rewrite([]string{"typo"}, r)
	if len(got) != 1 || got[0] != "typo" {
		t.Errorf("Rewrite() = %v, want it untouched so cobra can report the error", got)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestSplit|TestRewriteInfers'`
Expected: FAIL — `undefined: Resolver`, `app.splitGroup undefined`.

- [ ] **Step 7: Change Rewrite to take a Resolver**

Replace the body of `internal/cli/rewrite.go`:

```go
package cli

import "strings"

// completePrefix is the hidden command cobra's shell completion invokes.
const completePrefix = "__complete"

// Resolver supplies everything Rewrite needs to know about the world.
type Resolver struct {
	IsCommand  func(name string) bool
	IsGroup    func(name string) bool
	InferGroup func() (string, bool)
	IsWorkflow func(group, name string) bool
}

// Rewrite turns the group-first sugar into canonical verb-first arguments:
//
//	prizm XYZ up local  →  prizm up XYZ local
//	prizm XYZ local     →  prizm up XYZ local
//	prizm XYZ           →  prizm ls XYZ
//	prizm local         →  prizm up XYZ local   (inside one of XYZ's repos)
//
// Anything starting with a command or a flag is returned untouched, as is
// anything that cannot be resolved, so cobra reports the error itself.
func Rewrite(args []string, r Resolver) []string {
	if len(args) == 0 {
		return args
	}

	// Shell completion: rewrite the words the user actually typed.
	if args[0] == completePrefix {
		return append([]string{completePrefix}, Rewrite(args[1:], r)...)
	}

	head := args[0]
	if strings.HasPrefix(head, "-") || r.IsCommand(head) {
		return args
	}

	if r.IsGroup(head) {
		if len(args) == 1 {
			return []string{"ls", head}
		}
		if r.IsCommand(args[1]) {
			return append([]string{args[1], head}, args[2:]...)
		}
		return append([]string{"up", head}, args[1:]...)
	}

	// Not a command, not a group: it may be a workflow of the group we are
	// standing in.
	if group, ok := r.InferGroup(); ok && r.IsWorkflow(group, head) {
		return append([]string{"up", group, head}, args[1:]...)
	}
	return args
}
```

In `internal/cli/rewrite_test.go`, replace `testPredicates` and both call sites:

```go
func testResolver() Resolver {
	commands := map[string]bool{
		"init": true, "add-repo": true, "add-workflow": true,
		"up": true, "ls": true, "var": true, "import": true,
		"shared": true, "completion": true, "help": true, "__complete": true,
	}
	groups := map[string]bool{"XYZ": true, "ABC": true}

	return Resolver{
		IsCommand:  func(s string) bool { return commands[s] },
		IsGroup:    func(s string) bool { return groups[s] },
		InferGroup: func() (string, bool) { return "", false },
		IsWorkflow: func(string, string) bool { return false },
	}
}
```

and change every `Rewrite(tt.in, isCommand, isGroup)` to `Rewrite(tt.in, testResolver())`, deleting the `isCommand, isGroup := testPredicates()` lines.

- [ ] **Step 8: Implement the CLI resolution helpers**

Create `internal/cli/locate.go`:

```go
package cli

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// splitGroup separates the group from the remaining positional arguments.
// want is how many positionals the command needs after the group, so
// want+1 arguments means the group was named and want means infer it.
func (a *App) splitGroup(args []string, want int) (store.Group, []string, error) {
	if len(args) > want {
		g, err := a.mustGroup(args[0])
		if err != nil {
			return store.Group{}, nil, err
		}
		return g, args[1:], nil
	}

	_, g, err := a.locate()
	if err != nil {
		return store.Group{}, nil, err
	}
	return g, args, nil
}

// splitGroupRepo separates an optional group and an optional repo from the
// remaining positionals. Anything not given is inferred from the current
// directory; anything given wins over inference.
func (a *App) splitGroupRepo(args []string, want int) (store.Group, store.Repo, []string, error) {
	switch {
	case len(args) > want+1: // group and repo both named
		g, err := a.mustGroup(args[0])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		repo, err := a.Store.RepoByName(g.ID, args[1])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, fmt.Errorf("no such repo %q in group %s", args[1], g.Name)
		}
		return g, repo, args[2:], nil

	case len(args) > want: // repo named, group inferred
		_, g, err := a.locate()
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		repo, err := a.Store.RepoByName(g.ID, args[0])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, fmt.Errorf("no such repo %q in group %s", args[0], g.Name)
		}
		return g, repo, args[1:], nil

	default: // both inferred
		repo, g, err := a.locate()
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		return g, repo, args, nil
	}
}

// locate finds the repo and group containing the current directory.
func (a *App) locate() (store.Repo, store.Group, error) {
	cwd, err := a.Cwd()
	if err != nil {
		return store.Repo{}, store.Group{}, fmt.Errorf("determining current directory: %w", err)
	}

	repo, g, err := a.Store.RepoForPath(cwd)
	if err != nil {
		return store.Repo{}, store.Group{}, fmt.Errorf(
			"not inside a registered repo, so prizm cannot tell which group you mean — "+
				"name it explicitly (for example `prizm up <group> <workflow>`), or cd into one of the group's repos (cwd: %s)",
			cwd)
	}
	return repo, g, nil
}
```

- [ ] **Step 9: Make the commands accept the shorter forms**

In `internal/cli/up.go`, change the argument handling:

```go
		Use:   "up [group] <workflow>",
		Short: "Apply a workflow: build and link every covered repo's env file",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}
			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}
```

(the rest of the function body is unchanged).

In `internal/cli/vars.go`, `var` becomes:

```go
		Use:   "var [group] [repo] KEY=VALUE [KEY=VALUE...]",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			assignments := countAssignments(args)
			g, repo, rest, err := app.splitGroupRepo(args, assignments)
			if err != nil {
				return err
			}

			var wf store.Workflow
			if workflow != "" {
				wf, err = app.Store.WorkflowByName(g.ID, workflow)
				if err != nil {
					return fmt.Errorf("no such workflow %q in group %s", workflow, g.Name)
				}
			}

			for _, assignment := range rest {
				key, value, err := parseAssignment(assignment)
				if err != nil {
					return err
				}
				if workflow == "" {
					err = app.Store.SetRepoVar(repo.ID, key, value)
				} else {
					err = app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, key, value)
				}
				if err != nil {
					return err
				}
			}

			fmt.Fprintf(app.Out, "set %d variable(s) on %s/%s%s\n",
				len(rest), g.Name, repo.Name, scopeSuffix(workflow))
			return nil
		},
```

and `import` becomes `Use: "import [group] [repo] <file>"`, `Args: cobra.RangeArgs(1, 3)`, with `g, repo, rest, err := app.splitGroupRepo(args, 1)` and the file path read from `rest[0]`.

Add the assignment counter to `internal/cli/vars.go` — it is what lets `var` tell trailing `KEY=VALUE` pairs from leading group/repo names:

```go
// countAssignments returns how many trailing arguments look like KEY=VALUE.
// Everything before them is the optional group and repo.
func countAssignments(args []string) int {
	n := 0
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.Contains(args[i], "=") {
			break
		}
		n++
	}
	if n == 0 {
		return 1 // let parseAssignment produce the error message
	}
	return n
}
```

Delete the now-unused `resolveTarget` function.

- [ ] **Step 10: Update rewriteArgs to build a Resolver**

In `internal/cli/root.go`, replace `rewriteArgs`:

```go
func rewriteArgs(app *App, root *cobra.Command, args []string) []string {
	return Rewrite(args, Resolver{
		IsCommand: func(name string) bool {
			if name == completePrefix || name == "help" || name == "completion" {
				return true
			}
			for _, c := range root.Commands() {
				if c.Name() == name || c.HasAlias(name) {
					return true
				}
			}
			return false
		},
		IsGroup: func(name string) bool {
			_, err := app.Store.GroupByName(name)
			return err == nil
		},
		InferGroup: func() (string, bool) {
			cwd, err := app.Cwd()
			if err != nil {
				return "", false
			}
			_, g, err := app.Store.RepoForPath(cwd)
			if err != nil {
				return "", false
			}
			return g.Name, true
		},
		IsWorkflow: func(group, name string) bool {
			g, err := app.Store.GroupByName(group)
			if err != nil {
				return false
			}
			_, err = app.Store.WorkflowByName(g.ID, name)
			return err == nil
		},
	})
}
```

- [ ] **Step 11: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS — every package, including the updated rewrite tests.

- [ ] **Step 12: Try it from inside a repo**

```bash
go build -o /tmp/prizm .
cd /tmp/demo/backend
/tmp/prizm local            # no group, no verb
cd /tmp && /tmp/prizm up local   # expected: the ambiguity error
```

Expected: the first applies `XYZ/local`; the second fails with the "name it explicitly" message.

- [ ] **Step 13: Commit**

```bash
git add internal/store/ internal/cli/
git commit -m "feat(cli): infer group and repo from the current directory"
```

---

### Task 17: File-backed shared bags and `shared-sync`

**Files:**
- Create: `internal/sharedfile/file.go`, `internal/sharedfile/diff.go`, `internal/store/sharedfiles.go`, `internal/cli/shared.go`
- Modify: `internal/store/vars.go` (the `SharedGroup` struct and its two SELECTs), `internal/store/workflows.go` (reserved names), `internal/cli/root.go` (register commands)
- Test: `internal/sharedfile/file_test.go`, `internal/sharedfile/diff_test.go`, `internal/cli/shared_test.go`

**Interfaces:**
- Consumes: `envfile.Parse`, `envfile.Render` (Task 2); `config.DataDir`, `EnsureDir` (Task 1); store shared-group methods (Task 8); `App.Confirm`, `splitGroup` (Tasks 15–16).
- Produces:
  - `sharedfile.Render(repos []string, vars map[string]string) string` — the `# prizm:repos …` header plus `.env` body.
  - `sharedfile.Parse(text string) (repos []string, vars map[string]string, hasHeader bool, err error)`
  - `sharedfile.Change` struct: `Key, From, To string`; `sharedfile.Diff` struct: `Added []string`, `Removed []string`, `Changed []Change`; `sharedfile.(Diff).Empty() bool`; `sharedfile.Compare(current, incoming map[string]string) Diff`.
  - `store.SharedGroup.FilePath string` — new field.
  - `store.(*Store).SetSharedGroupFile(id int64, path string) error`
  - `store.(*Store).ListSharedGroups(workflowID int64) ([]SharedGroup, error)`
  - `store.(*Store).AllSharedGroups() ([]SharedGroupRef, error)` — `SharedGroupRef` is `SharedGroup` plus `GroupName`, `WorkflowName`.
  - `store.(*Store).SharedGroupRepos(id int64) ([]Repo, error)`
  - `store.(*Store).ReplaceSharedGroupVars(id int64, vars map[string]string) error` — full replace, in one transaction.
  - `store.(*Store).ReplaceSharedGroupRepos(id int64, repoIDs []int64) error`
  - Commands: `shared-add`, `shared-edit`, `shared-ls`, `shared-sync`.

The commands are flat and hyphenated (`shared-add`, not `shared add`) to keep the grammar invariant from Task 13 intact: the group is always the first positional, so the group-first sugar rewrite needs no per-command knowledge.

- [ ] **Step 1: Write the failing file-format test**

Create `internal/sharedfile/file_test.go`:

```go
package sharedfile

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRenderIncludesReposHeader(t *testing.T) {
	got := Render([]string{"backend", "auth", "ai"}, map[string]string{"_PRIZM_DB_USER": "svc"})

	if !strings.HasPrefix(got, "# prizm:repos backend,auth,ai\n") {
		t.Errorf("Render() = %q, want it to start with the repos header", got)
	}
	if !strings.Contains(got, "_PRIZM_DB_USER=svc") {
		t.Errorf("Render() = %q, want the variable body", got)
	}
}

func TestRenderWithoutReposOmitsHeader(t *testing.T) {
	got := Render(nil, map[string]string{"A": "1"})
	if strings.Contains(got, "prizm:repos") {
		t.Errorf("Render() = %q, want no header when there are no repos", got)
	}
}

func TestParseReadsHeaderAndVars(t *testing.T) {
	repos, vars, hasHeader, err := Parse(
		"# prizm:repos backend, auth ,ai\n\n_PRIZM_DB_USER=svc\n_PRIZM_DB_URL=postgres://${_PRIZM_DB_USER}@h/db\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !hasHeader {
		t.Error("hasHeader = false, want true")
	}
	if diff := cmp.Diff([]string{"backend", "auth", "ai"}, repos); diff != "" {
		t.Errorf("repos mismatch (-want +got):\n%s", diff)
	}

	want := map[string]string{
		"_PRIZM_DB_USER": "svc",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}@h/db",
	}
	if diff := cmp.Diff(want, vars); diff != "" {
		t.Errorf("vars mismatch (-want +got):\n%s", diff)
	}
}

func TestParseWithoutHeader(t *testing.T) {
	repos, vars, hasHeader, err := Parse("A=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if hasHeader {
		t.Error("hasHeader = true, want false")
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v, want none", repos)
	}
	if vars["A"] != "1" {
		t.Errorf("vars = %v, want A=1", vars)
	}
}

func TestParseIgnoresOrdinaryComments(t *testing.T) {
	_, vars, hasHeader, err := Parse("# just a note\nA=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if hasHeader {
		t.Error("an ordinary comment was mistaken for the prizm header")
	}
	if vars["A"] != "1" {
		t.Errorf("vars = %v, want A=1", vars)
	}
}

func TestParseEmptyReposHeader(t *testing.T) {
	repos, _, hasHeader, err := Parse("# prizm:repos\nA=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !hasHeader {
		t.Error("hasHeader = false, want true for an explicitly empty audience")
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v, want none", repos)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	repos := []string{"backend", "auth"}
	vars := map[string]string{"_PRIZM_A": "1", "_PRIZM_URL": "postgres://${_PRIZM_A}@h/db"}

	gotRepos, gotVars, _, err := Parse(Render(repos, vars))
	if err != nil {
		t.Fatalf("Parse(Render(...)) error = %v", err)
	}
	if diff := cmp.Diff(repos, gotRepos); diff != "" {
		t.Errorf("repos mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(vars, gotVars); diff != "" {
		t.Errorf("vars mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sharedfile/`
Expected: FAIL — `undefined: Render`, `undefined: Parse`.

- [ ] **Step 3: Implement the file format**

Create `internal/sharedfile/file.go`:

```go
// Package sharedfile is the on-disk format for a shared bag: ordinary .env
// text plus one optional header naming the repos that receive it.
package sharedfile

import (
	"strings"

	"github.com/troglodytto/prizm/internal/envfile"
)

// headerPrefix introduces the repo-audience line.
const headerPrefix = "# prizm:repos"

// Render writes a shared bag as editable text.
func Render(repos []string, vars map[string]string) string {
	var b strings.Builder

	if len(repos) > 0 {
		b.WriteString(headerPrefix + " " + strings.Join(repos, ",") + "\n\n")
	}
	b.WriteString(envfile.Render(vars))
	return b.String()
}

// Parse reads a shared bag file. hasHeader distinguishes "no audience stated"
// (membership stays CLI-managed) from "an explicitly empty audience".
func Parse(text string) ([]string, map[string]string, bool, error) {
	var (
		repos     []string
		hasHeader bool
		body      strings.Builder
	)

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))

		if strings.HasPrefix(trimmed, headerPrefix) {
			hasHeader = true
			for _, name := range strings.Split(strings.TrimPrefix(trimmed, headerPrefix), ",") {
				if name = strings.TrimSpace(name); name != "" {
					repos = append(repos, name)
				}
			}
			continue
		}

		body.WriteString(line)
		body.WriteByte('\n')
	}

	vars, err := envfile.Parse(body.String())
	if err != nil {
		return nil, nil, hasHeader, err
	}
	return repos, vars, hasHeader, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sharedfile/ -v`
Expected: PASS.

- [ ] **Step 5: Write the failing diff test**

Create `internal/sharedfile/diff_test.go`:

```go
package sharedfile

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCompareDetectsEveryKindOfChange(t *testing.T) {
	got := Compare(
		map[string]string{"KEEP": "same", "CHANGE": "old", "GONE": "x"},
		map[string]string{"KEEP": "same", "CHANGE": "new", "NEW": "y"},
	)

	if diff := cmp.Diff([]string{"NEW"}, got.Added); diff != "" {
		t.Errorf("Added mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"GONE"}, got.Removed); diff != "" {
		t.Errorf("Removed mismatch (-want +got):\n%s", diff)
	}
	want := []Change{{Key: "CHANGE", From: "old", To: "new"}}
	if diff := cmp.Diff(want, got.Changed); diff != "" {
		t.Errorf("Changed mismatch (-want +got):\n%s", diff)
	}
}

func TestCompareIsKeyLevelNotLineLevel(t *testing.T) {
	// Reordering keys is not a change. This is why prizm diffs maps, not text.
	got := Compare(
		map[string]string{"A": "1", "B": "2"},
		map[string]string{"B": "2", "A": "1"},
	)
	if !got.Empty() {
		t.Errorf("Compare() = %+v, want empty — key order must not register as drift", got)
	}
}

func TestCompareEmptyWhenIdentical(t *testing.T) {
	if !Compare(map[string]string{"A": "1"}, map[string]string{"A": "1"}).Empty() {
		t.Error("Compare(identical).Empty() = false, want true")
	}
}

func TestCompareResultsAreSorted(t *testing.T) {
	got := Compare(map[string]string{}, map[string]string{"Z": "1", "A": "2", "M": "3"})
	if diff := cmp.Diff([]string{"A", "M", "Z"}, got.Added); diff != "" {
		t.Errorf("Added mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/sharedfile/ -run TestCompare`
Expected: FAIL — `undefined: Compare`.

- [ ] **Step 7: Implement the diff**

Create `internal/sharedfile/diff.go`:

```go
package sharedfile

import "sort"

// Change is one key whose value differs.
type Change struct {
	Key  string
	From string
	To   string
}

// Diff is a key-level comparison of two variable maps. Env files are not
// prose: diffing per key means editor key-reordering is not mistaken for a
// change.
type Diff struct {
	Added   []string
	Removed []string
	Changed []Change
}

// Empty reports whether nothing differs.
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Compare diffs incoming against current. Results are sorted for stable output.
func Compare(current, incoming map[string]string) Diff {
	var d Diff

	for key, value := range incoming {
		old, ok := current[key]
		switch {
		case !ok:
			d.Added = append(d.Added, key)
		case old != value:
			d.Changed = append(d.Changed, Change{Key: key, From: old, To: value})
		}
	}
	for key := range current {
		if _, ok := incoming[key]; !ok {
			d.Removed = append(d.Removed, key)
		}
	}

	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Key < d.Changed[j].Key })
	return d
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/sharedfile/ -v`
Expected: PASS.

- [ ] **Step 9: Extend the store**

In `internal/store/vars.go`, add the field to `SharedGroup`:

```go
type SharedGroup struct {
	ID         int64
	WorkflowID int64
	Name       string
	FilePath   string
}
```

and select it in both existing queries — `SharedGroupByName`:

```go
	err := s.db.QueryRow(
		`SELECT id, workflow_id, name, file_path FROM shared_groups WHERE workflow_id = ? AND name = ?`,
		workflowID, name,
	).Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath)
```

and `SharedGroupsForRepo`:

```go
	rows, err := s.db.Query(`
		SELECT sg.id, sg.workflow_id, sg.name, sg.file_path
		FROM shared_groups sg
		JOIN shared_group_repos sgr ON sgr.shared_group_id = sg.id
		WHERE sg.workflow_id = ? AND sgr.repo_id = ?
		ORDER BY sg.name`, workflowID, repoID)
	...
		if err := rows.Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath); err != nil {
```

Create `internal/store/sharedfiles.go`:

```go
package store

import "time"

// SharedGroupRef is a shared bag with the names needed to address it.
type SharedGroupRef struct {
	SharedGroup
	GroupName    string
	WorkflowName string
}

// SetSharedGroupFile points a bag at the file that backs it.
func (s *Store) SetSharedGroupFile(id int64, path string) error {
	_, err := s.db.Exec(`UPDATE shared_groups SET file_path = ? WHERE id = ?`, path, id)
	return err
}

// ListSharedGroups returns a workflow's bags, ordered by name.
func (s *Store) ListSharedGroups(workflowID int64) ([]SharedGroup, error) {
	rows, err := s.db.Query(
		`SELECT id, workflow_id, name, file_path FROM shared_groups WHERE workflow_id = ? ORDER BY name`,
		workflowID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedGroup
	for rows.Next() {
		var sg SharedGroup
		if err := rows.Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// AllSharedGroups returns every bag in the database with its addressing names.
func (s *Store) AllSharedGroups() ([]SharedGroupRef, error) {
	rows, err := s.db.Query(`
		SELECT sg.id, sg.workflow_id, sg.name, sg.file_path, g.name, w.name
		FROM shared_groups sg
		JOIN workflows w ON w.id = sg.workflow_id
		JOIN "groups"  g ON g.id = w.group_id
		ORDER BY g.name, w.name, sg.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedGroupRef
	for rows.Next() {
		var ref SharedGroupRef
		if err := rows.Scan(
			&ref.ID, &ref.WorkflowID, &ref.Name, &ref.FilePath,
			&ref.GroupName, &ref.WorkflowName,
		); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// SharedGroupRepos returns a bag's member repos, ordered by name.
func (s *Store) SharedGroupRepos(id int64) ([]Repo, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file
		FROM repos r
		JOIN shared_group_repos sgr ON sgr.repo_id = r.id
		WHERE sgr.shared_group_id = ?
		ORDER BY r.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceSharedGroupVars makes the bag's variables exactly vars. The file is
// authoritative, so a key absent from it is deleted rather than merged.
func (s *Store) ReplaceSharedGroupVars(id int64, vars map[string]string) error {
	for key := range vars {
		if err := checkKey(key); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM shared_group_vars WHERE shared_group_id = ?`, id); err != nil {
		return err
	}

	now := time.Now().Unix()
	for key, value := range vars {
		blob, err := s.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO shared_group_vars(shared_group_id, key, value, updated_at) VALUES (?, ?, ?, ?)`,
			id, key, blob, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceSharedGroupRepos makes the bag's membership exactly repoIDs.
func (s *Store) ReplaceSharedGroupRepos(id int64, repoIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM shared_group_repos WHERE shared_group_id = ?`, id); err != nil {
		return err
	}
	for _, repoID := range repoIDs {
		if _, err := tx.Exec(
			`INSERT INTO shared_group_repos(shared_group_id, repo_id) VALUES (?, ?)`, id, repoID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

In `internal/store/workflows.go`, add the new verbs to `reservedNames`:

```go
	"shared-add": true, "shared-edit": true, "shared-ls": true, "shared-sync": true,
```

- [ ] **Step 10: Write the failing command test**

Create `internal/cli/shared_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func (h *harness) sharedFixture(t *testing.T) (beDir, authDir, file string) {
	t.Helper()

	beDir, authDir = h.repoDir(t, "backend"), h.repoDir(t, "auth")
	file = filepath.Join(t.TempDir(), "db.env")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "auth", "--path", authDir)
	h.run(t, "add-workflow", "XYZ", "local")

	if err := h.run(t, "shared-add", "XYZ", "local", "db", "--repos", "backend,auth", "--file", file); err != nil {
		t.Fatalf("shared-add error = %v", err)
	}
	return beDir, authDir, file
}

func TestSharedAddCreatesBagAndFile(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("bag file not created: %v", err)
	}
	if !strings.Contains(string(body), "# prizm:repos backend,auth") {
		t.Errorf("file = %q, want the repos header", body)
	}

	info, _ := os.Stat(file)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("bag file mode = %04o, want 0600 — it holds plaintext secrets", perm)
	}
}

func TestSharedSyncLoadsEditedFile(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte(
		"# prizm:repos backend,auth\n\n"+
			"_PRIZM_DB_USER=svc_app\n"+
			"_PRIZM_DB_URL=postgres://${_PRIZM_DB_USER}@h/db\n"), 0o600)

	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	got, _ := h.app.Store.SharedGroupVars(sg.ID)
	want := map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}@h/db",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("bag vars mismatch (-want +got):\n%s", diff)
	}
}

func TestSharedSyncIsAFullReplace(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\nB=2\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	// B deleted from the file must be deleted from the bag.
	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")
	got, _ := h.app.Store.SharedGroupVars(sg.ID)

	if _, still := got["B"]; still {
		t.Error("B survived; sync must be a full replace so the file stays authoritative")
	}
}

func TestSharedSyncReconcilesMembershipFromHeader(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend\n\nA=1\n"), 0o600)
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	repos, _ := h.app.Store.SharedGroupRepos(sg.ID)
	if len(repos) != 1 || repos[0].Name != "backend" {
		t.Errorf("members = %+v, want only backend after the header changed", repos)
	}
}

func TestSharedSyncShowsDiffAndRespectsDeclining(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	var prompted bool
	h.app.Confirm = func(string) (bool, error) {
		prompted = true
		return false, nil
	}

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	if !prompted {
		t.Error("shared-sync did not ask before writing")
	}
	if !strings.Contains(h.out.String(), "A") {
		t.Errorf("output = %q, want the key-level diff", h.out.String())
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")
	if got, _ := h.app.Store.SharedGroupVars(sg.ID); len(got) != 0 {
		t.Errorf("bag = %v, want unchanged after declining", got)
	}
}

func TestSharedSyncNoChangesIsQuietAndSucceeds(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called when nothing changed")
		return false, nil
	}
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("second shared-sync error = %v", err)
	}
	if !strings.Contains(h.out.String(), "up to date") {
		t.Errorf("output = %q, want an up-to-date message", h.out.String())
	}
}

func TestSharedSyncRejectsUnknownRepoInHeader(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,ghost\n\nA=1\n"), 0o600)

	err := h.run(t, "shared-sync", "XYZ", "local", "db")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown repo", err)
	}
}

func TestSharedLsShowsBagsAndFiles(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	if err := h.run(t, "shared-ls", "XYZ"); err != nil {
		t.Fatalf("shared-ls error = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "db") || !strings.Contains(out, file) {
		t.Errorf("output = %q, want the bag name and its file", out)
	}
}
```

- [ ] **Step 11: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestShared`
Expected: FAIL — unknown command `shared-add`.

- [ ] **Step 12: Implement the commands**

Create `internal/cli/shared.go`:

```go
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

func newSharedAddCmd(app *App) *cobra.Command {
	var (
		repoList string
		file     string
	)

	cmd := &cobra.Command{
		Use:   "shared-add [group] <workflow> <name>",
		Short: "Create a file-backed shared variable bag",
		Long: "The bag is backed by a real .env file you edit directly. Run\n" +
			"`prizm shared-sync` afterwards to reconcile your edits into prizm.",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 2)
			if err != nil {
				return err
			}
			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}
			name := rest[1]

			repoIDs, err := resolveRepoIDs(app, g, repoList)
			if err != nil {
				return err
			}

			path := file
			if path == "" {
				path, err = defaultBagPath(g.Name, wf.Name, name)
				if err != nil {
					return err
				}
			} else if !filepath.IsAbs(path) {
				if path, err = resolvePath(app, path); err != nil {
					return err
				}
			}

			sg, err := app.Store.CreateSharedGroup(wf.ID, name)
			if err != nil {
				return err
			}
			if err := app.Store.SetSharedGroupFile(sg.ID, path); err != nil {
				return err
			}
			if err := app.Store.ReplaceSharedGroupRepos(sg.ID, repoIDs); err != nil {
				return err
			}

			if err := writeBagFile(app, sg.ID, path); err != nil {
				return err
			}

			fmt.Fprintf(app.Out, "created shared bag %s/%s/%s\n  edit: %s\n  then: prizm shared-sync\n",
				g.Name, wf.Name, name, path)
			return nil
		},
	}

	cmd.Flags().StringVar(&repoList, "repos", "", "comma-separated repos that receive this bag (default: all in the group)")
	cmd.Flags().StringVar(&file, "file", "", "back the bag with this file (default: inside prizm's data directory)")
	return cmd
}

func newSharedEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "shared-edit [group] <workflow> <name>",
		Short: "Open a shared bag's file in $EDITOR",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, sg, err := resolveBag(app, args)
			if err != nil {
				return err
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			ed := exec.Command(editor, sg.FilePath)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return fmt.Errorf("running %s: %w", editor, err)
			}

			fmt.Fprintln(app.Out, "run `prizm shared-sync` to apply your edits")
			return nil
		},
	}
}

func newSharedLsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "shared-ls [group]",
		Short: "List shared bags and the files backing them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bags, err := app.Store.AllSharedGroups()
			if err != nil {
				return err
			}

			filter := ""
			if len(args) == 1 {
				g, err := app.mustGroup(args[0])
				if err != nil {
					return err
				}
				filter = g.Name
			}

			for _, bag := range bags {
				if filter != "" && bag.GroupName != filter {
					continue
				}
				repos, err := app.Store.SharedGroupRepos(bag.ID)
				if err != nil {
					return err
				}
				names := make([]string, 0, len(repos))
				for _, r := range repos {
					names = append(names, r.Name)
				}
				fmt.Fprintf(app.Out, "%s/%s/%s  %v\n  %s\n",
					bag.GroupName, bag.WorkflowName, bag.Name, names, bag.FilePath)
			}
			return nil
		},
	}
}

func newSharedSyncCmd(app *App) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "shared-sync [group] [workflow] [name]",
		Short: "Reconcile shared bag files into prizm",
		Long: "The file is the source of truth: a key removed from it is removed from\n" +
			"the bag. Nothing is written without confirmation.",
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			bags, err := selectBags(app, args)
			if err != nil {
				return err
			}
			if len(bags) == 0 {
				fmt.Fprintln(app.Out, "no shared bags to sync")
				return nil
			}

			for _, bag := range bags {
				if err := syncBag(app, bag, yes); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "apply without confirmation")
	return cmd
}

// syncBag reconciles one bag's file into the database.
func syncBag(app *App, bag store.SharedGroupRef, yes bool) error {
	label := fmt.Sprintf("%s/%s/%s", bag.GroupName, bag.WorkflowName, bag.Name)

	raw, err := os.ReadFile(bag.FilePath)
	if err != nil {
		return fmt.Errorf("reading %s for %s: %w", bag.FilePath, label, err)
	}

	repoNames, incoming, hasHeader, err := sharedfile.Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", bag.FilePath, err)
	}

	current, err := app.Store.SharedGroupVars(bag.ID)
	if err != nil {
		return err
	}
	diff := sharedfile.Compare(current, incoming)

	// Membership, only when the file states it.
	var (
		repoIDs        []int64
		membershipDiff string
	)
	if hasHeader {
		g, err := app.mustGroup(bag.GroupName)
		if err != nil {
			return err
		}
		for _, name := range repoNames {
			repo, err := app.Store.RepoByName(g.ID, name)
			if err != nil {
				return fmt.Errorf("%s: header names repo %q, which is not in group %s", bag.FilePath, name, g.Name)
			}
			repoIDs = append(repoIDs, repo.ID)
		}

		members, err := app.Store.SharedGroupRepos(bag.ID)
		if err != nil {
			return err
		}
		existing := make([]string, 0, len(members))
		for _, m := range members {
			existing = append(existing, m.Name)
		}
		if strings.Join(existing, ",") != strings.Join(repoNames, ",") {
			membershipDiff = fmt.Sprintf("  repos %v → %v\n", existing, repoNames)
		}
	}

	if diff.Empty() && membershipDiff == "" {
		fmt.Fprintf(app.Out, "%s up to date\n", label)
		return nil
	}

	fmt.Fprintf(app.Out, "%s ← %s\n", label, bag.FilePath)
	for _, key := range diff.Added {
		fmt.Fprintf(app.Out, "  + %s\n", key)
	}
	for _, c := range diff.Changed {
		fmt.Fprintf(app.Out, "  ~ %s  %s → %s\n", c.Key, c.From, c.To)
	}
	for _, key := range diff.Removed {
		fmt.Fprintf(app.Out, "  - %s\n", key)
	}
	fmt.Fprint(app.Out, membershipDiff)

	if !yes {
		ok, err := app.Confirm("Apply? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(app.Out, "%s skipped\n", label)
			return nil
		}
	}

	if err := app.Store.ReplaceSharedGroupVars(bag.ID, incoming); err != nil {
		return err
	}
	if hasHeader {
		if err := app.Store.ReplaceSharedGroupRepos(bag.ID, repoIDs); err != nil {
			return err
		}
	}

	fmt.Fprintf(app.Out, "%s synced\n", label)
	return nil
}

// selectBags narrows the bags to sync from optional group/workflow/name args.
func selectBags(app *App, args []string) ([]store.SharedGroupRef, error) {
	all, err := app.Store.AllSharedGroups()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return all, nil
	}

	g, rest, err := app.splitGroup(args, len(args)-1)
	if err != nil {
		return nil, err
	}

	var out []store.SharedGroupRef
	for _, bag := range all {
		if bag.GroupName != g.Name {
			continue
		}
		if len(rest) >= 1 && bag.WorkflowName != rest[0] {
			continue
		}
		if len(rest) >= 2 && bag.Name != rest[1] {
			continue
		}
		out = append(out, bag)
	}
	return out, nil
}

// resolveBag resolves exactly one bag from [group] <workflow> <name>.
func resolveBag(app *App, args []string) (store.Group, store.Workflow, store.SharedGroup, error) {
	g, rest, err := app.splitGroup(args, 2)
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{}, err
	}
	wf, err := app.Store.WorkflowByName(g.ID, rest[0])
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{},
			fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
	}
	sg, err := app.Store.SharedGroupByName(wf.ID, rest[1])
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{},
			fmt.Errorf("no such shared bag %q in %s/%s", rest[1], g.Name, wf.Name)
	}
	return g, wf, sg, nil
}

// defaultBagPath is where prizm keeps a bag file when the user did not choose.
func defaultBagPath(group, workflow, name string) (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shared", group, workflow, name+".env"), nil
}

// writeBagFile materialises a bag's current contents as editable text.
func writeBagFile(app *App, bagID int64, path string) error {
	if err := config.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}

	vars, err := app.Store.SharedGroupVars(bagID)
	if err != nil {
		return err
	}
	repos, err := app.Store.SharedGroupRepos(bagID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}

	// 0600: this file holds plaintext secrets while the DB copy is encrypted.
	return os.WriteFile(path, []byte(sharedfile.Render(names, vars)), 0o600)
}
```

- [ ] **Step 13: Register the commands**

Add to `root.AddCommand(...)` in `internal/cli/root.go`:

```go
		newSharedAddCmd(app),
		newSharedEditCmd(app),
		newSharedLsCmd(app),
		newSharedSyncCmd(app),
```

- [ ] **Step 14: Run the suite**

Run: `go test ./... `
Expected: PASS across every package.

- [ ] **Step 15: Commit**

```bash
git add internal/sharedfile/ internal/store/ internal/cli/
git commit -m "feat(shared): file-backed shared bags with key-level sync"
```

---

### Task 18: Dynamic shell completion

**Files:**
- Create: `internal/cli/completion.go`
- Modify: `internal/cli/root.go`, `internal/cli/up.go` (attach the completion functions)
- Test: `internal/cli/completion_test.go`

**Interfaces:**
- Consumes: `rank.Rank`, `rank.Candidate` (Task 10); `store.ListGroups`, `RepoPathsByGroup`, `ListWorkflows`, `GroupByName` (Tasks 5–7); `App.Cwd`, `App.Now` (Task 13).
- Produces:
  - `cli.(*App).completeGroups(toComplete string) ([]string, cobra.ShellCompDirective)` — directory-ranked group names.
  - `cli.(*App).completeWorkflows(group, toComplete string) ([]string, cobra.ShellCompDirective)`
  - `ValidArgsFunction` wired onto the root command and onto `up`.

Two things the spec calls out, both easy to get wrong:

- **`cobra.ShellCompDirectiveKeepOrder` is mandatory.** bash and zsh alphabetise candidates by default, which would silently discard the directory-relevance ranking — the exact thing the spec asked for. Without this directive the whole of Task 10 is invisible.
- **Completion runs on every Tab press**, synchronously, so it touches plaintext metadata columns only: no decryption, no keychain, no file walking.

Task 16's `Rewrite` already normalises `__complete` arguments, so by the time cobra dispatches, `prizm XYZ <TAB>` has become `up XYZ <TAB>` and only `up` needs a positional completer.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/completion_test.go`:

```go
package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

func TestCompleteGroupsRanksByDirectory(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "AAA") // alphabetically first, unrelated
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.cwd = beDir

	got, directive := h.app.completeGroups("")

	if len(got) != 2 {
		t.Fatalf("completeGroups() = %v, want both groups — it must sort, not filter", got)
	}
	if got[0] != "XYZ" {
		t.Errorf("completeGroups()[0] = %q, want %q for the group containing cwd", got[0], "XYZ")
	}
	if directive&cobra.ShellCompDirectiveKeepOrder == 0 {
		t.Error("directive lacks KeepOrder; the shell would alphabetise and discard the ranking")
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("directive lacks NoFileComp; the shell would offer filenames too")
	}
}

func TestCompleteGroupsWithNoGroups(t *testing.T) {
	h := newHarness(t)

	got, _ := h.app.completeGroups("")
	if len(got) != 0 {
		t.Errorf("completeGroups() = %v, want none", got)
	}
}

func TestCompleteWorkflows(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "add-workflow", "XYZ", "local")

	got, directive := h.app.completeWorkflows("XYZ", "")

	if diff := cmp.Diff([]string{"local", "production"}, got); diff != "" {
		t.Errorf("completeWorkflows() mismatch (-want +got):\n%s", diff)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("directive lacks NoFileComp")
	}
}

func TestCompleteWorkflowsUnknownGroup(t *testing.T) {
	h := newHarness(t)

	got, _ := h.app.completeWorkflows("NOPE", "")
	if len(got) != 0 {
		t.Errorf("completeWorkflows(unknown) = %v, want none", got)
	}
}

// `prizm up <TAB>` → group names; `prizm up XYZ <TAB>` → that group's workflows.
func TestUpCompletionByPosition(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "local")

	up := newUpCmd(h.app)

	first, _ := up.ValidArgsFunction(up, nil, "")
	if len(first) == 0 || first[0] != "XYZ" {
		t.Errorf("first position = %v, want the group names", first)
	}

	second, _ := up.ValidArgsFunction(up, []string{"XYZ"}, "")
	if diff := cmp.Diff([]string{"local"}, second); diff != "" {
		t.Errorf("second position mismatch (-want +got):\n%s", diff)
	}

	third, _ := up.ValidArgsFunction(up, []string{"XYZ", "local"}, "")
	if len(third) != 0 {
		t.Errorf("third position = %v, want nothing left to complete", third)
	}
}

// Standing inside a repo, the first position offers that group's workflows too,
// because `prizm up local` is valid there.
func TestUpCompletionOffersWorkflowsWhenGroupIsInferable(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.cwd = beDir

	up := newUpCmd(h.app)
	got, _ := up.ValidArgsFunction(up, nil, "")

	var sawWorkflow bool
	for _, c := range got {
		if c == "local" {
			sawWorkflow = true
		}
	}
	if !sawWorkflow {
		t.Errorf("first position = %v, want it to include the inferred group's workflows", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestComplete|TestUpCompletion'`
Expected: FAIL — `h.app.completeGroups undefined`.

- [ ] **Step 3: Implement the completers**

Create `internal/cli/completion.go`:

```go
package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/rank"
)

// completeGroups returns every group name, ordered by relevance to the current
// directory. The spec is explicit that this sorts rather than filters, so
// KeepOrder is required — without it the shell alphabetises and the ranking is
// silently thrown away.
func (a *App) completeGroups(toComplete string) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder

	groups, err := a.Store.ListGroups()
	if err != nil || len(groups) == 0 {
		return nil, directive
	}
	paths, err := a.Store.RepoPathsByGroup()
	if err != nil {
		return nil, directive
	}

	cwd, err := a.Cwd()
	if err != nil {
		cwd = ""
	}

	candidates := make([]rank.Candidate, 0, len(groups))
	for _, g := range groups {
		candidates = append(candidates, rank.Candidate{
			Name:       g.Name,
			Paths:      paths[g.Name],
			UseCount:   g.UseCount,
			LastUsedAt: g.LastUsedAt,
		})
	}

	return withPrefix(rank.Rank(candidates, cwd, a.Now()), toComplete), directive
}

// completeWorkflows returns one group's workflow names.
func (a *App) completeWorkflows(group, toComplete string) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp

	g, err := a.Store.GroupByName(group)
	if err != nil {
		return nil, directive
	}
	workflows, err := a.Store.ListWorkflows(g.ID)
	if err != nil {
		return nil, directive
	}

	names := make([]string, 0, len(workflows))
	for _, w := range workflows {
		names = append(names, w.Name)
	}
	return withPrefix(names, toComplete), directive
}

// inferredGroupName is the group containing the current directory, if any.
func (a *App) inferredGroupName() (string, bool) {
	cwd, err := a.Cwd()
	if err != nil {
		return "", false
	}
	_, g, err := a.Store.RepoForPath(cwd)
	if err != nil {
		return "", false
	}
	return g.Name, true
}

// completeRoot handles `prizm <TAB>`: groups first, then verbs.
func (a *App) completeRoot(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	out, directive := a.completeGroups(toComplete)

	if group, ok := a.inferredGroupName(); ok {
		workflows, _ := a.completeWorkflows(group, toComplete)
		out = append(out, workflows...)
	}
	for _, c := range cmd.Commands() {
		if !c.Hidden {
			out = append(out, c.Name())
		}
	}
	return withPrefix(out, toComplete), directive
}

// withPrefix keeps only candidates the user's partial word could become,
// preserving order.
func withPrefix(candidates []string, toComplete string) []string {
	if toComplete == "" {
		return candidates
	}

	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.HasPrefix(c, toComplete) {
			out = append(out, c)
		}
	}
	return out
}
```

- [ ] **Step 4: Attach the completers**

In `internal/cli/up.go`, add to the `up` command definition:

```go
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				// Either a group name, or — inside a repo — a workflow directly.
				out, directive := app.completeGroups(toComplete)
				if group, ok := app.inferredGroupName(); ok {
					workflows, _ := app.completeWorkflows(group, toComplete)
					out = append(out, workflows...)
				}
				return out, directive
			case 1:
				return app.completeWorkflows(args[0], toComplete)
			default:
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
		},
```

In `internal/cli/root.go`, add to the root command definition:

```go
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return app.completeRoot(cmd, toComplete)
		},
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 6: Verify completion end to end in a real shell**

```bash
go build -o /tmp/prizm .
/tmp/prizm completion fish > ~/.config/fish/completions/prizm.fish
exec fish
cd /tmp/demo/backend
prizm <TAB>          # DEMO first, plus local; verbs after
prizm DEMO <TAB>     # local
```

Also check the raw protocol, which is what the shell actually calls:

```bash
/tmp/prizm __complete DEMO ""
```

Expected: workflow names, then a `:` directive line whose value has both `KeepOrder` (32) and `NoFileComp` (4) bits set.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): directory-aware dynamic shell completion"
```

---

## What This Phase Delivers

After Task 18 the following works end to end:

```bash
prizm init XYZ
prizm add-repo XYZ frontend --path ~/code/frontend
prizm add-repo XYZ backend  --path ~/code/backend
prizm add-repo XYZ auth     --path ~/code/auth
prizm add-workflow XYZ local                              # all three repos
prizm add-workflow XYZ frontend-only --repos frontend
prizm add-workflow XYZ production --tag prod

prizm import XYZ backend ~/code/backend/.env.local --workflow local
prizm var XYZ frontend API_URL=http://localhost:8080 --workflow local

prizm shared-add XYZ local db --repos backend,auth
$EDITOR ~/.local/share/prizm/shared/XYZ/local/db.env      # the derived credentials
prizm shared-sync

prizm var XYZ backend DB_URL='${_PRIZM_DB_URL}' --workflow local
prizm var XYZ auth DATABASE_URL='${_PRIZM_DB_URL}' --workflow local

cd ~/code/backend
prizm local                                               # group, verb both inferred
prizm <TAB>                                               # XYZ first, ranked by cwd
```

## Deferred to Later Phases

Everything below is designed in the spec and deliberately out of this phase. Nothing here requires a schema change that Phase 1 has not already made.

| Feature | Why it is not here | What Phase 1 already provides for it |
| --- | --- | --- |
| Bubble Tea TUI picker (`prizm` bare, fuzzy list) | The whole Charm stack is one coherent chunk of work | `rank.Rank` is renderer-agnostic; `ls` is the placeholder entry point |
| `prizm sync` (repo file → prizm) | The conflict-resolution UX is the hard part | Layers stay separately queryable; `ForRepo` returns templates; `Emit` is a distinct step |
| Divergence warning on `up` | Needs `sync`'s comparison to point at | `sharedfile.Compare` is the comparison |
| `prizm status` | Wants the TUI to be worth reading | The `applied` table is populated by `RecordApplied` on every `up` |
| `prizm audit` + version carousel | Needs snapshot-on-write plus a Charm carousel | Every write path funnels through the store; hashing a resolved map is cheap |
| Docker compose bring-up, `prizm down` | Separate lifecycle, separate failure modes | Workflows already exist as the natural attachment point |
| `--dry-run` | Same diff machinery as `sync` | `Expand`/`Emit` produce the candidate map without writing |
| `prizm repair` (a repo moved) | Explicitly "for later" in the spec | Paths are stored in exactly one place, `repos.path` |
| Concurrency guard for two simultaneous `up`s | Known rough edge, not worth solving yet | Symlink swap is atomic, so the worst case is last-write-wins |

## Self-Review

**1. Spec coverage.** Every settled decision in `prizm-design-brainstorm-transcript.md` maps to a task or to the deferred table above:

| Spec decision | Where |
| --- | --- |
| Go, not Rust | Global Constraints |
| Cobra + dynamic completion, `KeepOrder` | Task 18 |
| SQLite as source of truth | Task 4 |
| Encrypt values, not the whole DB; metadata stays queryable | Tasks 3, 8 |
| Group → repos (fixed paths) → workflows | Tasks 5–7 |
| Workflows replace environments; explicit repo subsets | Task 7 |
| Optional `--tag` for guardrails | Tasks 7, 14 |
| Three variable layers, most-specific wins | Tasks 8, 9 |
| Shared groups for cross-repo duplication | Tasks 8, 9, 16 |
| Interpolation and derived shared values | Interpolation section, Task 9 |
| Internal values absent from the emitted file | Task 9 (`Emit`) |
| `import` as the on-ramp from existing `.env` files | Task 14 |
| Build then symlink | Task 11 |
| Backup before overwrite | Task 11 |
| Repo paths never move without `repair` | Task 6, deferred table |
| Run from anywhere; ambiguity is an error for now | Task 16 |
| Consistent, colour-degrading output | Task 12 |
| Key-level diffs, never line-level | Task 17 |
| Divergence is reported, never auto-resolved | Task 17, Phase 2 Constraint section |

**2. Placeholder scan.** No task contains "TBD", "implement later", "add error handling", "similar to Task N", or a test step without runnable test code. Every code step carries the actual code.

**3. Type consistency.** Cross-checked: `resolve.Emit` takes one argument (`expanded`) in both its definition and its use in `up.go`; `apply.Result` field names match their readers; `store.SharedGroup` gains `FilePath` in Task 17 with both of its existing queries updated; Task 16 replaces `Rewrite`'s three-argument form everywhere it appears, including the Task 13 tests; `resolveTarget` is deleted in Task 16 when `splitGroupRepo` supersedes it; `App.Confirm` is added in Task 15 along with the test-harness default. `resolveRepoIDs` and `resolvePath` are defined once (Tasks 12) and reused in Task 17.

**Two things a reviewer should watch for during execution:**

- Task 4's schema is applied with `CREATE TABLE IF NOT EXISTS` and no migration table. That is correct for a greenfield tool, but the moment Phase 2 changes a column, a real migration mechanism is needed — do not paper over it with more `IF NOT EXISTS`.
- Task 16 changes the signature of a function three earlier tasks call. Run `go build ./...` immediately after Step 7 of that task rather than waiting for the test step; the compiler will list every call site to update.
