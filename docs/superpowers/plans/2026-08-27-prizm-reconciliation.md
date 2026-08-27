# prizm Phase 2 — Reconciliation & Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give prizm memory and eyesight — record every variable change as a snapshot, detect when a repo's env file has drifted from what prizm would generate, show that state with `prizm status`, and reconcile hand-edits back into the right layer with `prizm sync`.

**Architecture:** Three new pure-ish engines sitting on Phase 1's store. `resolve` gains *attribution* — which layer defined the winning template for a key — because reconciliation is meaningless without knowing where a value came from. `drift` compares what is on disk against what `up` would write right now. `syncplan` turns a drift report into a list of classified, explainable actions. The commands (`status`, `sync`, `dry-run`, `repair`) are thin renderers over those three. Everything in this phase is plain text; Phase 3 replaces the rendering, not the logic.

**Tech Stack:** Unchanged from Phase 1 — Go 1.23, Cobra, cgo SQLite (`mattn/go-sqlite3`), `google/go-cmp` for tests. No new dependencies.

**Spec:** `prizm-design-brainstorm-transcript.md`, plus the "Phase 2 Constraint: `prizm sync` Semantics" section of `2026-08-27-prizm-core.md`.

**Prerequisite:** Phase 1 complete (`docs/superpowers/plans/2026-08-27-prizm-core.md`, Tasks 1–18).

## Global Constraints

- All Phase 1 constraints still hold. In particular: values encrypted at rest, metadata plaintext, store holds templates never expansions, `_PRIZM_` prefix marks internal, repo paths only change via `repair`.
- **Divergence is never auto-resolved.** `up` warns and finishes; `sync` is the only command that reconciles, and only when invoked.
- **No mtime.** The spec's own warning: a `git pull` or an IDE reformat bumps mtime without changing a value. Every comparison in this phase is over `{key: value}` maps, never timestamps and never file text.
- **Key-level diffs only.** Reordering keys in a file is not a change.
- **Nothing destructive without confirmation**, and `--yes` is the only way to skip it.
- **All output goes through `internal/style`** (Phase 1 Task 12). `status`, `sync` and the dry run print the same glyphs, the same column, the same tag colours as `up`.
- This phase adds the **first schema migration**. Phase 1 shipped `CREATE TABLE IF NOT EXISTS` with no version tracking; Task 1 fixes that before adding a single new table.

---

## Design: Attribution and the Derived-Value Problem

`sync` reads a repo's env file and asks "what did the human change?" Answering that is easy. Knowing *where to write it back* is the hard part, and one case is genuinely ambiguous.

A repo's `.env` is a symlink to a generated file, so editing it edits prizm's output. Reconciliation is therefore:

```
onDisk   = parse(repoPath/envFile)                    -- what the human left there
expected = Emit(Expand(Merge(layers)))                -- what up would write now
diff     = Compare(expected, onDisk)                  -- the human's edits
```

For each changed key, prizm attributes it to the layer that defined the winning template, then classifies:

| The winning template | Where the edit goes | Why |
| --- | --- | --- |
| Key is new on disk | Repo+workflow layer | Nothing else could own it |
| Literal, owned by the repo's own layer | Repo+workflow layer | Unambiguous |
| Literal, owned by a **shared bag** | The bag — **after** naming every other repo it will change | This is the propagation the spec wants, but it is not a quiet write |
| Contains `${...}` | **Ambiguous. Ask.** | See below |
| Key deleted on disk | Removed from its owning layer | Confirmed, and the owner is named |

**The ambiguous case.** Suppose `backend` has `DB_URL = ${_PRIZM_DB_URL}` and the human edits the resulting line in `backend/.env` to point at a different host. Two completely different intentions produce that same edit:

1. *"The shared database moved."* → update `_PRIZM_DB_URL` in the bag, which changes backend, auth and ai together.
2. *"Just backend talks to a different host."* → pin the literal on backend's own layer, breaking backend's link to the shared value.

Writing back the literal — the naive implementation — silently picks (2) and severs a derivation the user spent effort building. So prizm refuses to guess:

```
$ prizm sync
backend/.env → XYZ/local/backend
  ~ DB_URL   postgres://old-host/app → postgres://new-host/app
      this value comes from ${_PRIZM_DB_URL} in shared bag 'db'
      [s] update the shared value  (also changes: auth, ai)
      [p] pin this literal on backend only  (backend stops tracking 'db')
      [k] skip
```

That prompt is the reason Phase 3 exists, and it is also why this phase ships the classification without the interactive picker: non-interactively, an ambiguous key is **skipped and reported**, and `--pin` opts into (2) explicitly for scripts. Nothing is guessed, and nothing is lost — the user can always edit the bag file directly.

**Composite templates** (`API=${_PRIZM_HOST}/v1`) offer only the pin option, because prizm cannot reliably invert the substitution to work out which part the human meant to change. The message says so and points at the bag file.

---

### Task 1: Schema migrations and the snapshot engine

**Files:**
- Modify: `internal/store/store.go` (replace one-shot schema with versioned migrations)
- Create: `internal/store/migrate.go`, `internal/store/snapshots.go`
- Test: `internal/store/migrate_test.go`, `internal/store/snapshots_test.go`

**Interfaces:**
- Consumes: Phase 1 `Store`, `cipher`, `ErrNotFound`.
- Produces:
  - `store.(*Store).migrate() error` — applies pending migrations, tracked in `PRAGMA user_version`.
  - `store.ScopeKind` (`store.ScopeWorkflowRepo`, `store.ScopeSharedGroup`) and `store.Scope` struct: `Kind ScopeKind`, `A int64`, `B int64`.
  - `store.WorkflowRepoScope(workflowID, repoID int64) Scope`, `store.SharedGroupScope(id int64) Scope`.
  - `store.Snapshot` struct: `ID int64`, `Scope Scope`, `Source string`, `Note string`, `CreatedAt time.Time`.
  - `store.(*Store).RecordSnapshot(scope Scope, vars map[string]string, source, note string, now time.Time) (bool, error)` — returns `false` when the content hash matches the previous snapshot, so re-running `up` cannot spam identical entries.
  - `store.(*Store).ListSnapshots(scope Scope) ([]Snapshot, error)` — newest first.
  - `store.(*Store).SnapshotVars(id int64) (map[string]string, error)`

Snapshot sources are a small closed set: `var`, `import`, `sync`, `shared-sync`, `restore`. The spec is explicit that *why* a snapshot happened is often more useful during an audit than the diff itself.

- [ ] **Step 1: Write the failing migration test**

Create `internal/store/migrate_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/crypto"
)

func TestMigrateSetsUserVersion(t *testing.T) {
	s := newTestStore(t)

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestMigrateIsIdempotentAcrossReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	for i := 0; i < 3; i++ {
		s, err := Open(path, crypto.Plaintext{})
		if err != nil {
			t.Fatalf("Open() run %d error = %v", i, err)
		}
		s.Close()
	}
}

// A database created by Phase 1 has user_version 0 but already has the tables.
// Re-running migration 1 must not fail.
func TestMigrateUpgradesAPhase1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatalf("resetting user_version: %v", err)
	}
	s.Close()

	s2, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("reopening a version-0 database error = %v", err)
	}
	defer s2.Close()

	var version int
	s2.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d after upgrade", version, len(migrations))
	}
}

func TestSnapshotTablesExist(t *testing.T) {
	s := newTestStore(t)

	for _, table := range []string{"snapshots", "snapshot_vars"} {
		var name string
		if err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMigrate`
Expected: FAIL — `undefined: migrations`.

- [ ] **Step 3: Convert the schema to versioned migrations**

Create `internal/store/migrate.go`:

```go
package store

import "fmt"

// migrations are applied in order. The index+1 is the schema version, tracked
// in PRAGMA user_version. Never edit a released migration — append a new one.
var migrations = []string{
	// 1: the Phase 1 schema. Idempotent, so a Phase 1 database (user_version 0)
	// upgrades cleanly without recreating anything.
	schema,

	// 2: snapshot history. Every variable write records the resulting map, so
	// `prizm audit` has something to read once it exists.
	`
CREATE TABLE IF NOT EXISTS snapshots (
	id           INTEGER PRIMARY KEY,
	scope_kind   TEXT    NOT NULL,
	scope_a      INTEGER NOT NULL,
	scope_b      INTEGER NOT NULL,
	content_hash TEXT    NOT NULL,
	source       TEXT    NOT NULL,
	note         TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_scope
	ON snapshots(scope_kind, scope_a, scope_b, created_at DESC);

CREATE TABLE IF NOT EXISTS snapshot_vars (
	snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
	key         TEXT    NOT NULL,
	value       BLOB    NOT NULL,
	PRIMARY KEY (snapshot_id, key)
);
`,
}

// migrate brings the database up to len(migrations).
func (s *Store) migrate() error {
	var current int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		if _, err := s.db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("applying migration %d: %w", i+1, err)
		}
		// user_version does not accept a bound parameter.
		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("recording schema version %d: %w", i+1, err)
		}
	}
	return nil
}
```

In `internal/store/store.go`, replace the schema application inside `Open`:

```go
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
```

with:

```go
	s := &Store{db: db, cipher: c}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
```

and change the tail of `Open` to secure the file and return `s`:

```go
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("securing database file: %w", err)
	}
	return s, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestMigrate|TestSnapshotTables' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing snapshot test**

Create `internal/store/snapshots_test.go`:

```go
package store

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var snapNow = time.Unix(1700000000, 0)

func TestRecordAndListSnapshots(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	scope := WorkflowRepoScope(wf.ID, r.ID)

	wrote, err := s.RecordSnapshot(scope, map[string]string{"A": "1"}, "var", "", snapNow)
	if err != nil {
		t.Fatalf("RecordSnapshot() error = %v", err)
	}
	if !wrote {
		t.Error("RecordSnapshot() = false, want true for the first snapshot")
	}

	snaps, err := s.ListSnapshots(scope)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("ListSnapshots() = %d snapshots, want 1", len(snaps))
	}
	if snaps[0].Source != "var" {
		t.Errorf("Source = %q, want %q", snaps[0].Source, "var")
	}

	vars, err := s.SnapshotVars(snaps[0].ID)
	if err != nil {
		t.Fatalf("SnapshotVars() error = %v", err)
	}
	if diff := cmp.Diff(map[string]string{"A": "1"}, vars); diff != "" {
		t.Errorf("SnapshotVars() mismatch (-want +got):\n%s", diff)
	}
}

// Re-running `up` re-resolves the same map every time; identical snapshots
// would drown the audit log.
func TestRecordSnapshotSkipsIdenticalContent(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	scope := WorkflowRepoScope(wf.ID, r.ID)

	vars := map[string]string{"A": "1", "B": "2"}
	s.RecordSnapshot(scope, vars, "var", "", snapNow)

	wrote, err := s.RecordSnapshot(scope, vars, "up", "", snapNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("RecordSnapshot() error = %v", err)
	}
	if wrote {
		t.Error("RecordSnapshot() = true for identical content, want false")
	}

	snaps, _ := s.ListSnapshots(scope)
	if len(snaps) != 1 {
		t.Errorf("ListSnapshots() = %d, want 1 — identical content must not add a version", len(snaps))
	}
}

// Key order must not affect the hash.
func TestRecordSnapshotHashIsOrderIndependent(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	scope := WorkflowRepoScope(wf.ID, r.ID)

	s.RecordSnapshot(scope, map[string]string{"A": "1", "B": "2"}, "var", "", snapNow)
	wrote, _ := s.RecordSnapshot(scope, map[string]string{"B": "2", "A": "1"}, "var", "", snapNow)

	if wrote {
		t.Error("a reordered map produced a different hash")
	}
}

func TestRecordSnapshotWritesWhenContentChanges(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	scope := WorkflowRepoScope(wf.ID, r.ID)

	s.RecordSnapshot(scope, map[string]string{"A": "1"}, "var", "", snapNow)
	s.RecordSnapshot(scope, map[string]string{"A": "2"}, "sync", "reconciled from backend", snapNow.Add(time.Hour))

	snaps, _ := s.ListSnapshots(scope)
	if len(snaps) != 2 {
		t.Fatalf("ListSnapshots() = %d, want 2", len(snaps))
	}
	if snaps[0].Source != "sync" {
		t.Errorf("newest Source = %q, want %q — list must be newest first", snaps[0].Source, "sync")
	}
	if snaps[0].Note != "reconciled from backend" {
		t.Errorf("Note = %q, want the recorded note", snaps[0].Note)
	}
}

func TestSnapshotScopesAreIndependent(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	local, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	prod, _ := s.AddWorkflow(g.ID, "production", "", []int64{r.ID})

	s.RecordSnapshot(WorkflowRepoScope(local.ID, r.ID), map[string]string{"A": "1"}, "var", "", snapNow)
	s.RecordSnapshot(WorkflowRepoScope(prod.ID, r.ID), map[string]string{"A": "9"}, "var", "", snapNow)

	localSnaps, _ := s.ListSnapshots(WorkflowRepoScope(local.ID, r.ID))
	if len(localSnaps) != 1 {
		t.Errorf("local snapshots = %d, want 1 — scopes must not interleave", len(localSnaps))
	}
}

func TestSnapshotValuesAreEncrypted(t *testing.T) {
	s := newEncryptedTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.RecordSnapshot(WorkflowRepoScope(wf.ID, r.ID), map[string]string{"P": "SUPERSECRET"}, "var", "", snapNow)

	var blob []byte
	s.db.QueryRow(`SELECT value FROM snapshot_vars LIMIT 1`).Scan(&blob)
	if string(blob) == "SUPERSECRET" {
		t.Error("snapshot value stored in plaintext")
	}
}

func TestListSnapshotsEmptyScope(t *testing.T) {
	s := newTestStore(t)

	snaps, err := s.ListSnapshots(WorkflowRepoScope(1, 1))
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("ListSnapshots() = %v, want none", snaps)
	}
}
```

Add the encrypted-store helper to `internal/store/store_test.go`:

```go
func newEncryptedTestStore(t *testing.T) *Store {
	t.Helper()

	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := crypto.NewAESGCM(key)
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}

	s, err := Open(filepath.Join(t.TempDir(), "prizm.db"), c)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestRecord|TestSnapshot|TestList'`
Expected: FAIL — `undefined: WorkflowRepoScope`.

- [ ] **Step 7: Implement the snapshot engine**

Create `internal/store/snapshots.go`:

```go
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ScopeKind names what a snapshot belongs to.
type ScopeKind string

const (
	// ScopeWorkflowRepo is one repo's variables within one workflow.
	ScopeWorkflowRepo ScopeKind = "workflow_repo"
	// ScopeSharedGroup is one shared bag's variables.
	ScopeSharedGroup ScopeKind = "shared_group"
)

// Snapshot sources. A closed set: knowing *why* a version exists is usually
// more useful during an audit than the diff alone.
const (
	SourceVar        = "var"
	SourceImport     = "import"
	SourceSync       = "sync"
	SourceSharedSync = "shared-sync"
	SourceRestore    = "restore"
)

// Scope addresses a snapshot timeline.
type Scope struct {
	Kind ScopeKind
	A    int64
	B    int64
}

// WorkflowRepoScope is the timeline for one repo inside one workflow.
func WorkflowRepoScope(workflowID, repoID int64) Scope {
	return Scope{Kind: ScopeWorkflowRepo, A: workflowID, B: repoID}
}

// SharedGroupScope is the timeline for one shared bag.
func SharedGroupScope(id int64) Scope {
	return Scope{Kind: ScopeSharedGroup, A: id}
}

// Snapshot is one recorded version of a scope's variables.
type Snapshot struct {
	ID        int64
	Scope     Scope
	Source    string
	Note      string
	CreatedAt time.Time
}

// RecordSnapshot stores vars as a new version, unless it is byte-identical to
// the previous one. Reports whether it wrote.
func (s *Store) RecordSnapshot(scope Scope, vars map[string]string, source, note string, now time.Time) (bool, error) {
	hash := hashVars(vars)

	var last string
	err := s.db.QueryRow(`
		SELECT content_hash FROM snapshots
		WHERE scope_kind = ? AND scope_a = ? AND scope_b = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		string(scope.Kind), scope.A, scope.B,
	).Scan(&last)

	switch {
	case err == nil && last == hash:
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO snapshots(scope_kind, scope_a, scope_b, content_hash, source, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(scope.Kind), scope.A, scope.B, hash, source, note, now.Unix())
	if err != nil {
		return false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}

	for key, value := range vars {
		blob, err := s.cipher.Encrypt(value)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(
			`INSERT INTO snapshot_vars(snapshot_id, key, value) VALUES (?, ?, ?)`, id, key, blob,
		); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ListSnapshots returns a scope's versions, newest first.
func (s *Store) ListSnapshots(scope Scope) ([]Snapshot, error) {
	rows, err := s.db.Query(`
		SELECT id, source, note, created_at FROM snapshots
		WHERE scope_kind = ? AND scope_a = ? AND scope_b = ?
		ORDER BY created_at DESC, id DESC`,
		string(scope.Kind), scope.A, scope.B)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var (
			snap    Snapshot
			created int64
		)
		if err := rows.Scan(&snap.ID, &snap.Source, &snap.Note, &created); err != nil {
			return nil, err
		}
		snap.Scope = scope
		snap.CreatedAt = time.Unix(created, 0)
		out = append(out, snap)
	}
	return out, rows.Err()
}

// SnapshotVars returns one version's variables, decrypted.
func (s *Store) SnapshotVars(id int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM snapshot_vars WHERE snapshot_id = ?`, id)
}

// hashVars fingerprints a variable map independently of key order.
func hashVars(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%d:%s=%d:%s\n", len(k), k, len(vars[k]), vars[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS — every Phase 1 store test plus the new migration and snapshot tests.

- [ ] **Step 9: Commit**

```bash
git add internal/store/
git commit -m "feat(store): versioned migrations and content-hashed snapshot history"
```

---

### Task 2: Record a snapshot on every write path

**Files:**
- Modify: `internal/store/vars.go` (the three `Set*` methods and `ReplaceSharedGroupVars`)
- Test: `internal/store/snapshot_wiring_test.go`

**Interfaces:**
- Consumes: `RecordSnapshot` (Task 1).
- Produces:
  - `store.(*Store).SetWorkflowRepoVarWithSource(workflowID, repoID int64, key, value, source string) error`
  - `store.(*Store).ReplaceSharedGroupVarsWithSource(id int64, vars map[string]string, source, note string) error`
  - The existing `SetRepoVar`, `SetWorkflowRepoVar` and `ReplaceSharedGroupVars` keep their signatures and delegate with `SourceVar` / `SourceSharedSync`.

Snapshots are taken **after** the write, of the whole resulting scope map — not of the single key that changed. A version is a complete state, which is what makes both diffing and restore trivial later.

Note the asymmetry: the repo-shared layer (Layer 1) has no snapshot timeline of its own, because a snapshot scope must correspond to something a user would audit, and the spec scopes audit to `(repo, workflow)`. A Layer 1 change shows up in the timeline of every workflow that touches the repo, the next time one of them is written or applied.

- [ ] **Step 1: Write the failing test**

Create `internal/store/snapshot_wiring_test.go`:

```go
package store

import "testing"

func TestSetWorkflowRepoVarRecordsSnapshot(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.SetWorkflowRepoVar(wf.ID, r.ID, "A", "1")
	s.SetWorkflowRepoVar(wf.ID, r.ID, "B", "2")

	snaps, err := s.ListSnapshots(WorkflowRepoScope(wf.ID, r.ID))
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("ListSnapshots() = %d, want 2", len(snaps))
	}

	// Each version is the full state, not the single changed key.
	newest, _ := s.SnapshotVars(snaps[0].ID)
	if len(newest) != 2 || newest["A"] != "1" || newest["B"] != "2" {
		t.Errorf("newest snapshot = %v, want the complete map", newest)
	}
	oldest, _ := s.SnapshotVars(snaps[1].ID)
	if len(oldest) != 1 {
		t.Errorf("oldest snapshot = %v, want just A", oldest)
	}
}

func TestSetWorkflowRepoVarWithSourceTagsTheVersion(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	if err := s.SetWorkflowRepoVarWithSource(wf.ID, r.ID, "A", "1", SourceImport); err != nil {
		t.Fatalf("SetWorkflowRepoVarWithSource() error = %v", err)
	}

	snaps, _ := s.ListSnapshots(WorkflowRepoScope(wf.ID, r.ID))
	if snaps[0].Source != SourceImport {
		t.Errorf("Source = %q, want %q", snaps[0].Source, SourceImport)
	}
}

func TestSettingTheSameValueTwiceMakesOneVersion(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.SetWorkflowRepoVar(wf.ID, r.ID, "A", "1")
	s.SetWorkflowRepoVar(wf.ID, r.ID, "A", "1")

	snaps, _ := s.ListSnapshots(WorkflowRepoScope(wf.ID, r.ID))
	if len(snaps) != 1 {
		t.Errorf("ListSnapshots() = %d, want 1 — an unchanged write is not a version", len(snaps))
	}
}

func TestReplaceSharedGroupVarsRecordsSnapshot(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)
	sg, _ := s.CreateSharedGroup(wf.ID, "db")

	if err := s.ReplaceSharedGroupVarsWithSource(
		sg.ID, map[string]string{"_PRIZM_DB_USER": "svc"}, SourceSharedSync, "from db.env",
	); err != nil {
		t.Fatalf("ReplaceSharedGroupVarsWithSource() error = %v", err)
	}

	snaps, _ := s.ListSnapshots(SharedGroupScope(sg.ID))
	if len(snaps) != 1 {
		t.Fatalf("ListSnapshots() = %d, want 1", len(snaps))
	}
	if snaps[0].Source != SourceSharedSync || snaps[0].Note != "from db.env" {
		t.Errorf("snapshot = %+v, want the shared-sync source and note", snaps[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestSetWorkflowRepoVarRecords|TestReplaceSharedGroupVarsRecords'`
Expected: FAIL — `s.SetWorkflowRepoVarWithSource undefined`, and no snapshots recorded.

- [ ] **Step 3: Wire snapshots into the write paths**

In `internal/store/vars.go`, replace `SetWorkflowRepoVar` with a delegating pair:

```go
// SetWorkflowRepoVar upserts the highest-precedence layer.
func (s *Store) SetWorkflowRepoVar(workflowID, repoID int64, key, value string) error {
	return s.SetWorkflowRepoVarWithSource(workflowID, repoID, key, value, SourceVar)
}

// SetWorkflowRepoVarWithSource upserts and records a snapshot tagged with what
// caused the write.
func (s *Store) SetWorkflowRepoVarWithSource(workflowID, repoID int64, key, value, source string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		INSERT INTO workflow_repo_vars(workflow_id, repo_id, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workflow_id, repo_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		workflowID, repoID, key, blob, time.Now().Unix()); err != nil {
		return err
	}

	return s.snapshotWorkflowRepo(workflowID, repoID, source, "")
}

// DeleteWorkflowRepoVar removes a key from the highest-precedence layer.
func (s *Store) DeleteWorkflowRepoVar(workflowID, repoID int64, key, source string) error {
	if _, err := s.db.Exec(
		`DELETE FROM workflow_repo_vars WHERE workflow_id = ? AND repo_id = ? AND key = ?`,
		workflowID, repoID, key,
	); err != nil {
		return err
	}
	return s.snapshotWorkflowRepo(workflowID, repoID, source, "")
}

// snapshotWorkflowRepo records the resulting state of one (workflow, repo).
func (s *Store) snapshotWorkflowRepo(workflowID, repoID int64, source, note string) error {
	vars, err := s.WorkflowRepoVars(workflowID, repoID)
	if err != nil {
		return err
	}
	_, err = s.RecordSnapshot(WorkflowRepoScope(workflowID, repoID), vars, source, note, time.Now())
	return err
}
```

In `internal/store/sharedfiles.go`, do the same for the bag replace:

```go
// ReplaceSharedGroupVars makes the bag's variables exactly vars.
func (s *Store) ReplaceSharedGroupVars(id int64, vars map[string]string) error {
	return s.ReplaceSharedGroupVarsWithSource(id, vars, SourceSharedSync, "")
}

// ReplaceSharedGroupVarsWithSource replaces and records a tagged snapshot. The
// file is authoritative, so a key absent from vars is deleted, not merged.
func (s *Store) ReplaceSharedGroupVarsWithSource(id int64, vars map[string]string, source, note string) error {
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
	if err := tx.Commit(); err != nil {
		return err
	}

	_, err = s.RecordSnapshot(SharedGroupScope(id), vars, source, note, time.Now())
	return err
}
```

In `internal/cli/vars.go`, make `import` tag its writes — replace the `SetWorkflowRepoVar` call inside `newImportCmd` with:

```go
				if workflow == "" {
					err = app.Store.SetRepoVar(repo.ID, key, value)
				} else {
					err = app.Store.SetWorkflowRepoVarWithSource(wf.ID, repo.ID, key, value, store.SourceImport)
				}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... `
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ internal/cli/
git commit -m "feat(store): snapshot every variable write with its source"
```

---

### Task 3: Layer attribution

**Files:**
- Modify: `internal/resolve/merge.go` (the `Layer` struct), `internal/resolve/resolve.go`
- Create: `internal/resolve/attribute.go`
- Test: `internal/resolve/attribute_test.go`

**Interfaces:**
- Consumes: Phase 1 `resolve.Layer`, `ForRepo`, `Merge`; `store.SharedGroupsForRepo`.
- Produces:
  - `resolve.LayerKind` (`LayerRepoShared`, `LayerSharedGroup`, `LayerWorkflowRepo`) and two new `Layer` fields: `Kind LayerKind`, `SharedGroupID int64`.
  - `resolve.ForRepoLayers(s *store.Store, wf store.Workflow, repo store.Repo) ([]Layer, error)` — the layers, lowest precedence first, unmerged.
  - `resolve.ForRepo(...)` — unchanged signature, now `Merge(ForRepoLayers(...))`.
  - `resolve.Origin` struct: `Layer string`, `Kind LayerKind`, `SharedGroupID int64`, `Template string`.
  - `resolve.Attribute(layers []Layer, key string) (Origin, bool)` — which layer defined the winning template.
  - `resolve.SoleRef(template string) (string, bool)` — the name `X` when the template is *exactly* `${X}`, which is the only shape `sync` can safely invert.

- [ ] **Step 1: Write the failing test**

Create `internal/resolve/attribute_test.go`:

```go
package resolve

import "testing"

func layersFixture() []Layer {
	return []Layer{
		{Name: "repo-shared", Kind: LayerRepoShared, Vars: map[string]string{
			"LOG_LEVEL": "debug",
			"SHADOWED":  "from-repo",
		}},
		{Name: "shared:db", Kind: LayerSharedGroup, SharedGroupID: 7, Vars: map[string]string{
			"_PRIZM_DB_URL": "postgres://h/db",
			"SHADOWED":    "from-bag",
		}},
		{Name: "local+backend", Kind: LayerWorkflowRepo, Vars: map[string]string{
			"DB_URL":   "${_PRIZM_DB_URL}",
			"SHADOWED": "from-workflow",
		}},
	}
}

func TestAttributeFindsTheDefiningLayer(t *testing.T) {
	layers := layersFixture()

	tests := []struct {
		key       string
		wantLayer string
		wantKind  LayerKind
	}{
		{key: "LOG_LEVEL", wantLayer: "repo-shared", wantKind: LayerRepoShared},
		{key: "_PRIZM_DB_URL", wantLayer: "shared:db", wantKind: LayerSharedGroup},
		{key: "DB_URL", wantLayer: "local+backend", wantKind: LayerWorkflowRepo},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			origin, ok := Attribute(layers, tt.key)
			if !ok {
				t.Fatalf("Attribute(%q) not found", tt.key)
			}
			if origin.Layer != tt.wantLayer {
				t.Errorf("Layer = %q, want %q", origin.Layer, tt.wantLayer)
			}
			if origin.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", origin.Kind, tt.wantKind)
			}
		})
	}
}

func TestAttributeReturnsTheWinningLayerNotTheFirst(t *testing.T) {
	origin, ok := Attribute(layersFixture(), "SHADOWED")
	if !ok {
		t.Fatal("Attribute(SHADOWED) not found")
	}
	if origin.Layer != "local+backend" {
		t.Errorf("Layer = %q, want the highest-precedence definition", origin.Layer)
	}
	if origin.Template != "from-workflow" {
		t.Errorf("Template = %q, want %q", origin.Template, "from-workflow")
	}
}

func TestAttributeCarriesTheSharedGroupID(t *testing.T) {
	origin, _ := Attribute(layersFixture(), "_PRIZM_DB_URL")
	if origin.SharedGroupID != 7 {
		t.Errorf("SharedGroupID = %d, want 7 — sync needs it to write back", origin.SharedGroupID)
	}
}

func TestAttributeUnknownKey(t *testing.T) {
	if _, ok := Attribute(layersFixture(), "NOPE"); ok {
		t.Error("Attribute(unknown) ok = true, want false")
	}
}

func TestSoleRef(t *testing.T) {
	tests := []struct {
		template string
		want     string
		wantOK   bool
	}{
		{template: "${_PRIZM_DB_URL}", want: "_PRIZM_DB_URL", wantOK: true},
		{template: "  ${_PRIZM_DB_URL}  ", want: "_PRIZM_DB_URL", wantOK: true},
		{template: "postgres://${_PRIZM_HOST}/db", wantOK: false},
		{template: "${A}${B}", wantOK: false},
		{template: "plain", wantOK: false},
		{template: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			got, ok := SoleRef(tt.template)
			if ok != tt.wantOK {
				t.Fatalf("SoleRef(%q) ok = %v, want %v", tt.template, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("SoleRef(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolve/ -run 'TestAttribute|TestSoleRef'`
Expected: FAIL — `undefined: Attribute`, `undefined: LayerRepoShared`.

- [ ] **Step 3: Extend Layer and implement attribution**

In `internal/resolve/merge.go`, replace the `Layer` declaration:

```go
// LayerKind identifies which of the three variable layers this is.
type LayerKind int

const (
	// LayerRepoShared applies in every workflow that touches the repo.
	LayerRepoShared LayerKind = iota
	// LayerSharedGroup is a named bag scoped to (workflow, repo subset).
	LayerSharedGroup
	// LayerWorkflowRepo is one repo inside one workflow. Highest precedence.
	LayerWorkflowRepo
)

// Layer is one contributor to a repo's variables. Name appears in errors and
// in sync's explanations; SharedGroupID is set only for LayerSharedGroup.
type Layer struct {
	Name          string
	Kind          LayerKind
	SharedGroupID int64
	Vars          map[string]string
}
```

Create `internal/resolve/attribute.go`:

```go
package resolve

import "strings"

// Origin describes where a key's winning value was defined.
type Origin struct {
	Layer         string
	Kind          LayerKind
	SharedGroupID int64
	Template      string
}

// Attribute returns the highest-precedence layer defining key. layers must be
// in the same low-to-high order Merge expects.
func Attribute(layers []Layer, key string) (Origin, bool) {
	for i := len(layers) - 1; i >= 0; i-- {
		template, ok := layers[i].Vars[key]
		if !ok {
			continue
		}
		return Origin{
			Layer:         layers[i].Name,
			Kind:          layers[i].Kind,
			SharedGroupID: layers[i].SharedGroupID,
			Template:      template,
		}, true
	}
	return Origin{}, false
}

// SoleRef reports the referenced name when template is exactly one reference
// and nothing else. This is the only template shape sync can invert: given a
// new expanded value, the whole of it is the referenced variable's new value.
func SoleRef(template string) (string, bool) {
	trimmed := strings.TrimSpace(template)

	match := refPattern.FindStringSubmatch(trimmed)
	if match == nil || match[0] != trimmed {
		return "", false
	}
	return match[1], true
}
```

- [ ] **Step 4: Split ForRepo into layers plus a merge**

Replace `internal/resolve/resolve.go`:

```go
package resolve

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// ForRepoLayers assembles a repo's variable layers, lowest precedence first,
// without merging them. Reconciliation needs the layers intact so it can tell
// where each key came from.
func ForRepoLayers(s *store.Store, wf store.Workflow, repo store.Repo) ([]Layer, error) {
	repoVars, err := s.RepoVars(repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading repo-shared vars for %q: %w", repo.Name, err)
	}
	layers := []Layer{{Name: "repo-shared", Kind: LayerRepoShared, Vars: repoVars}}

	sharedGroups, err := s.SharedGroupsForRepo(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading shared groups for %q: %w", repo.Name, err)
	}
	for _, sg := range sharedGroups {
		vars, err := s.SharedGroupVars(sg.ID)
		if err != nil {
			return nil, fmt.Errorf("reading shared group %q: %w", sg.Name, err)
		}
		layers = append(layers, Layer{
			Name:          "shared:" + sg.Name,
			Kind:          LayerSharedGroup,
			SharedGroupID: sg.ID,
			Vars:          vars,
		})
	}

	specific, err := s.WorkflowRepoVars(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s vars: %w", wf.Name, repo.Name, err)
	}
	layers = append(layers, Layer{
		Name: wf.Name + "+" + repo.Name,
		Kind: LayerWorkflowRepo,
		Vars: specific,
	})

	return layers, nil
}

// ForRepo merges a repo's layers. The result still contains ${...} templates;
// call Expand to resolve them and Emit to drop the internal plumbing.
func ForRepo(s *store.Store, wf store.Workflow, repo store.Repo) (map[string]string, error) {
	layers, err := ForRepoLayers(s, wf, repo)
	if err != nil {
		return nil, err
	}
	return Merge(layers), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/resolve/ -v`
Expected: PASS — Phase 1 resolve tests plus the new attribution tests.

- [ ] **Step 6: Commit**

```bash
git add internal/resolve/
git commit -m "feat(resolve): expose layers and attribute each key to its defining layer"
```

---

### Task 4: The drift engine

**Files:**
- Create: `internal/drift/drift.go`
- Test: `internal/drift/drift_test.go`

**Interfaces:**
- Consumes: `envfile.Parse`, `sharedfile.Compare`/`Diff`, `store.Repo`.
- Produces:
  - `drift.LinkState` (`NoFile`, `Managed`, `ManagedElsewhere`, `Unmanaged`, `PathMissing`) with a `String()` method.
  - `drift.Report` struct: `Repo store.Repo`, `Link LinkState`, `LinkDest string`, `Diff sharedfile.Diff`.
  - `drift.(Report).InSync() bool`
  - `drift.Inspect(repo store.Repo, expected map[string]string, expectedBuilt string) (Report, error)`

Separating *link* state from *content* state matters: a repo can be correctly linked but content-drifted (someone edited the file), or content-identical but linked to another workflow's build (someone ran a different `up` elsewhere). Collapsing those into one status would lose exactly the information `status` exists to show.

- [ ] **Step 1: Write the failing test**

Create `internal/drift/drift_test.go`:

```go
package drift

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/store"
)

type fixture struct {
	repo  store.Repo
	built string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	root := t.TempDir()
	repoPath := filepath.Join(root, "backend")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	built := filepath.Join(root, "built", "backend.env")
	if err := os.MkdirAll(filepath.Dir(built), 0o700); err != nil {
		t.Fatalf("mkdir built: %v", err)
	}

	return fixture{
		repo:  store.Repo{Name: "backend", Path: repoPath, EnvFile: ".env"},
		built: built,
	}
}

func (f fixture) link(t *testing.T, content string) {
	t.Helper()

	if err := os.WriteFile(f.built, []byte(content), 0o600); err != nil {
		t.Fatalf("writing built file: %v", err)
	}
	if err := os.Symlink(f.built, filepath.Join(f.repo.Path, ".env")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func TestInspectInSync(t *testing.T) {
	f := newFixture(t)
	f.link(t, "A=1\nB=2\n")

	got, err := Inspect(f.repo, map[string]string{"A": "1", "B": "2"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !got.InSync() {
		t.Errorf("InSync() = false, want true (link=%v diff=%+v)", got.Link, got.Diff)
	}
}

func TestInspectDetectsContentDrift(t *testing.T) {
	f := newFixture(t)
	f.link(t, "A=9\nNEW=x\n")

	got, err := Inspect(f.repo, map[string]string{"A": "1", "GONE": "y"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != Managed {
		t.Errorf("Link = %v, want Managed", got.Link)
	}
	if got.InSync() {
		t.Fatal("InSync() = true, want false")
	}

	if len(got.Diff.Changed) != 1 || got.Diff.Changed[0].Key != "A" {
		t.Errorf("Changed = %+v, want A", got.Diff.Changed)
	}
	if len(got.Diff.Added) != 1 || got.Diff.Added[0] != "NEW" {
		t.Errorf("Added = %v, want [NEW]", got.Diff.Added)
	}
	if len(got.Diff.Removed) != 1 || got.Diff.Removed[0] != "GONE" {
		t.Errorf("Removed = %v, want [GONE]", got.Diff.Removed)
	}
}

// Reordering keys is not drift.
func TestInspectIgnoresKeyOrder(t *testing.T) {
	f := newFixture(t)
	f.link(t, "B=2\nA=1\n")

	got, _ := Inspect(f.repo, map[string]string{"A": "1", "B": "2"}, f.built)
	if !got.InSync() {
		t.Errorf("InSync() = false; key order must not register as drift (%+v)", got.Diff)
	}
}

func TestInspectNoFile(t *testing.T) {
	f := newFixture(t)

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != NoFile {
		t.Errorf("Link = %v, want NoFile", got.Link)
	}
	if got.InSync() {
		t.Error("InSync() = true for an unapplied repo")
	}
}

func TestInspectUnmanagedRegularFile(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(filepath.Join(f.repo.Path, ".env"), []byte("A=1\n"), 0o600)

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != Unmanaged {
		t.Errorf("Link = %v, want Unmanaged", got.Link)
	}
	// Content is still compared, so status can say whether it matters.
	if !got.Diff.Empty() {
		t.Errorf("Diff = %+v, want empty — the content happens to match", got.Diff)
	}
}

func TestInspectLinkedElsewhere(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "production.env")
	os.WriteFile(other, []byte("A=1\n"), 0o600)
	os.Symlink(other, filepath.Join(f.repo.Path, ".env"))

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != ManagedElsewhere {
		t.Errorf("Link = %v, want ManagedElsewhere", got.Link)
	}
	if got.LinkDest != other {
		t.Errorf("LinkDest = %q, want %q", got.LinkDest, other)
	}
}

func TestInspectMissingRepoPath(t *testing.T) {
	repo := store.Repo{Name: "gone", Path: filepath.Join(t.TempDir(), "nope"), EnvFile: ".env"}

	got, err := Inspect(repo, map[string]string{"A": "1"}, "/tmp/built.env")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != PathMissing {
		t.Errorf("Link = %v, want PathMissing", got.Link)
	}
}

func TestInspectUnparseableFileIsAnError(t *testing.T) {
	f := newFixture(t)
	f.link(t, "this is not an env file\n")

	if _, err := Inspect(f.repo, map[string]string{}, f.built); err == nil {
		t.Error("Inspect() error = nil, want a parse error naming the problem")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/drift/`
Expected: FAIL — `undefined: Inspect`.

- [ ] **Step 3: Implement the engine**

Create `internal/drift/drift.go`:

```go
// Package drift compares what is on disk against what prizm would write now.
package drift

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

// LinkState describes the repo's env file as a filesystem object.
type LinkState int

const (
	// NoFile means the workflow has never been applied here.
	NoFile LinkState = iota
	// Managed means the symlink points at the build prizm expects.
	Managed
	// ManagedElsewhere means it is a symlink to a different build — usually
	// another workflow was applied more recently.
	ManagedElsewhere
	// Unmanaged means a real file sits where prizm's symlink should be.
	Unmanaged
	// PathMissing means the repo directory is gone.
	PathMissing
)

func (l LinkState) String() string {
	switch l {
	case NoFile:
		return "not applied"
	case Managed:
		return "linked"
	case ManagedElsewhere:
		return "linked elsewhere"
	case Unmanaged:
		return "unmanaged file"
	case PathMissing:
		return "path missing"
	}
	return "unknown"
}

// Report is one repo's state relative to a workflow.
type Report struct {
	Repo     store.Repo
	Link     LinkState
	LinkDest string
	Diff     sharedfile.Diff
}

// InSync reports whether nothing needs doing.
func (r Report) InSync() bool { return r.Link == Managed && r.Diff.Empty() }

// Inspect compares repo's env file against expected, the map `up` would write.
func Inspect(repo store.Repo, expected map[string]string, expectedBuilt string) (Report, error) {
	report := Report{Repo: repo}

	if info, err := os.Stat(repo.Path); err != nil || !info.IsDir() {
		report.Link = PathMissing
		return report, nil
	}

	target := filepath.Join(repo.Path, repo.EnvFile)

	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		report.Link = NoFile
		return report, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("inspecting %s: %w", target, err)
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		dest, err := os.Readlink(target)
		if err != nil {
			return Report{}, fmt.Errorf("reading link %s: %w", target, err)
		}
		report.LinkDest = dest
		if dest == expectedBuilt {
			report.Link = Managed
		} else {
			report.Link = ManagedElsewhere
		}
	default:
		report.Link = Unmanaged
	}

	// Read through the link. A dangling symlink counts as not applied.
	raw, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		report.Link = NoFile
		return report, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("reading %s: %w", target, err)
	}

	onDisk, err := envfile.Parse(string(raw))
	if err != nil {
		return Report{}, fmt.Errorf("parsing %s: %w", target, err)
	}

	report.Diff = sharedfile.Compare(expected, onDisk)
	return report, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/drift/ -v`
Expected: PASS — all eight tests.

- [ ] **Step 5: Commit**

```bash
git add internal/drift/
git commit -m "feat(drift): compare on-disk env files against what up would write"
```

---

### Task 5: `prizm status`

**Files:**
- Create: `internal/cli/status.go`
- Modify: `internal/cli/root.go` (register), `internal/store/store.go` (`AppliedFor`)
- Test: `internal/cli/status_test.go`

**Interfaces:**
- Consumes: `drift.Inspect`, `resolve.ForRepo`/`Expand`/`Emit`, `config.BuiltPath`, `store.ListRepos`.
- Produces:
  - `store.Applied` struct: `RepoID int64`, `WorkflowID int64`, `BuiltPath string`, `AppliedAt time.Time`.
  - `store.(*Store).AppliedFor(groupID int64) (map[int64]Applied, error)` — keyed by repo ID.
  - `prizm status [group]` — per repo: which workflow it is on, its link state, and whether it has drifted.

The spec predicted this would be the first thing missed once several workflows exist: after a few days you cannot remember which workflow each repo is sitting on.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/status_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusShowsAppliedWorkflowPerRepo(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	feDir := h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "add-workflow", "XYZ", "frontend-only", "--repos", "frontend")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "var", "XYZ", "frontend", "B=2", "--workflow", "frontend-only")

	h.run(t, "up", "XYZ", "local")
	h.run(t, "up", "XYZ", "frontend-only")

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}

	out := h.out.String()
	for _, want := range []string{"backend", "local", "frontend", "frontend-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output = %q, want it to mention %q", out, want)
		}
	}
}

func TestStatusFlagsDrift(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")

	// Hand-edit the file the symlink points at.
	link := filepath.Join(beDir, ".env")
	dest, _ := os.Readlink(link)
	os.WriteFile(dest, []byte("A=9\n"), 0o600)

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "drift") {
		t.Errorf("status output = %q, want it to report drift", out)
	}
	if !strings.Contains(out, "sync") {
		t.Errorf("status output = %q, want it to point at `prizm sync`", out)
	}
}

func TestStatusShowsUnappliedRepos(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(h.out.String(), "not applied") {
		t.Errorf("status output = %q, want it to say the repo is not applied", h.out.String())
	}
}

func TestStatusFlagsMissingPath(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	os.RemoveAll(beDir)

	if err := h.run(t, "status", "XYZ"); err != nil {
		t.Fatalf("status error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "path missing") {
		t.Errorf("status output = %q, want it to report the missing path", out)
	}
	if !strings.Contains(out, "repair") {
		t.Errorf("status output = %q, want it to point at `prizm repair`", out)
	}
}

func TestStatusInfersGroupFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.cwd = beDir

	if err := h.run(t, "status"); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(h.out.String(), "backend") {
		t.Errorf("status output = %q, want the inferred group's repos", h.out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestStatus`
Expected: FAIL — unknown command `status`.

- [ ] **Step 3: Add the applied-state reader**

Append to `internal/store/store.go`:

```go
// Applied records which workflow a repo is currently linked to.
type Applied struct {
	RepoID     int64
	WorkflowID int64
	BuiltPath  string
	AppliedAt  time.Time
}

// AppliedFor returns the applied state of every repo in a group, keyed by repo ID.
func (s *Store) AppliedFor(groupID int64) (map[int64]Applied, error) {
	rows, err := s.db.Query(`
		SELECT a.repo_id, a.workflow_id, a.built_path, a.applied_at
		FROM applied a
		JOIN repos r ON r.id = a.repo_id
		WHERE r.group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]Applied)
	for rows.Next() {
		var (
			a       Applied
			applied int64
		)
		if err := rows.Scan(&a.RepoID, &a.WorkflowID, &a.BuiltPath, &applied); err != nil {
			return nil, err
		}
		a.AppliedAt = time.Unix(applied, 0)
		out[a.RepoID] = a
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implement the command**

Create `internal/cli/status.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/drift"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
)

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status [group]",
		Short: "Show which workflow each repo is on, and whether it has drifted",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}

			repos, err := app.Store.ListRepos(g.ID)
			if err != nil {
				return err
			}
			applied, err := app.Store.AppliedFor(g.ID)
			if err != nil {
				return err
			}
			workflows, err := app.Store.ListWorkflows(g.ID)
			if err != nil {
				return err
			}
			byID := make(map[int64]store.Workflow, len(workflows))
			for _, w := range workflows {
				byID[w.ID] = w
			}

			fmt.Fprintln(app.Out, g.Name)

			var sawDrift, sawMissing bool
			for _, repo := range repos {
				state, ok := applied[repo.ID]
				if !ok {
					fmt.Fprintf(app.Out, "  %-14s → (not applied)\n", repo.Name)
					continue
				}

				wf := byID[state.WorkflowID]
				report, err := inspectRepo(app, g, wf, repo)
				if err != nil {
					return err
				}

				tag := ""
				if wf.Tag != "" {
					tag = "  " + style.Tag(wf.Tag)
				}
				note := ""
				switch {
				case report.Link == drift.PathMissing:
					note, sawMissing = "  "+style.Warn.Glyph()+" path missing", true
				case report.Link == drift.ManagedElsewhere:
					note = "  " + style.Warn.Glyph() + " linked to another build"
				case report.Link == drift.Unmanaged:
					note = "  " + style.Warn.Glyph() + " unmanaged file, not prizm's link"
				case !report.Diff.Empty():
					note, sawDrift = fmt.Sprintf("  %s drift (%d change(s))", style.Warn.Glyph(), driftCount(report)), true
				}

				fmt.Fprintf(app.Out, "  %-*s → %-16s %s%s%s\n",
					style.NameWidth, repo.Name, wf.Name, report.Link, tag, note)
			}

			if sawDrift {
				fmt.Fprintln(app.Out, "\n"+style.Hint("run `prizm sync` to reconcile drifted repos"))
			}
			if sawMissing {
				fmt.Fprintln(app.Out, style.Hint("run `prizm repair` to re-point a repo whose path moved"))
			}
			return nil
		},
	}
}

// inspectRepo resolves what up would write for a repo, then compares it to disk.
func inspectRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) (drift.Report, error) {
	templates, err := resolve.ForRepo(app.Store, wf, repo)
	if err != nil {
		return drift.Report{}, err
	}

	// An unresolvable repo cannot be compared; report it as not-applied rather
	// than failing the whole status listing.
	expanded, err := resolve.Expand(templates)
	if err != nil {
		return drift.Report{Repo: repo, Link: drift.NoFile}, nil
	}

	builtPath, err := config.BuiltPath(g.Name, wf.Name, repo.Name)
	if err != nil {
		return drift.Report{}, err
	}
	return drift.Inspect(repo, resolve.Emit(expanded), builtPath)
}

func driftCount(r drift.Report) int {
	return len(r.Diff.Added) + len(r.Diff.Removed) + len(r.Diff.Changed)
}
```

Register it in `internal/cli/root.go`: `newStatusCmd(app),`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ internal/store/
git commit -m "feat(cli): status — applied workflow, link state and drift per repo"
```

---

### Task 6: `up` — divergence warning and `--dry-run`

**Files:**
- Modify: `internal/cli/up.go`
- Test: `internal/cli/up_dryrun_test.go`

**Interfaces:**
- Consumes: `drift.Inspect`, `inspectRepo` (Task 5), `sharedfile.Compare`.
- Produces: `prizm up ... --dry-run`, plus a pre-apply warning when a repo has hand-edits that the apply is about to overwrite.

The spec is precise about the shape of this: `up` performs a **cheap, read-only** check, prints a warning, points at `sync`, and **finishes anyway**. It never prompts and never refuses. The warning that actually matters is not "these files differ" — of course they differ, `up` is about to rewrite them — but "**you have local edits here that this apply will destroy.**"

- [ ] **Step 1: Write the failing test**

Create `internal/cli/up_dryrun_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (h *harness) seedApplied(t *testing.T) string {
	t.Helper()

	beDir := h.repoDir(t, "backend")
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")
	return beDir
}

func TestUpWarnsBeforeOverwritingLocalEdits(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)

	link := filepath.Join(beDir, ".env")
	dest, _ := os.Readlink(link)
	os.WriteFile(dest, []byte("A=EDITED\n"), 0o600)

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v — a warning must not fail the apply", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "sync") {
		t.Errorf("output = %q, want a warning pointing at `prizm sync`", out)
	}

	// It warns, then applies anyway.
	body, _ := os.ReadFile(link)
	if string(body) != "A=1\n" {
		t.Errorf("file = %q, want prizm's value — up warns but never refuses", body)
	}
}

func TestUpIsQuietWhenThereAreNoLocalEdits(t *testing.T) {
	h := newHarness(t)
	h.seedApplied(t)

	h.run(t, "up", "XYZ", "local")
	if strings.Contains(h.out.String(), "sync") {
		t.Errorf("output = %q, want no divergence warning", h.out.String())
	}
}

func TestUpDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local", "--dry-run"); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}

	if _, err := os.Lstat(filepath.Join(beDir, ".env")); !os.IsNotExist(err) {
		t.Error("--dry-run created a file")
	}
	if !strings.Contains(h.out.String(), "A") {
		t.Errorf("output = %q, want the would-be changes", h.out.String())
	}
}

func TestUpDryRunShowsPerKeyChanges(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)
	_ = beDir

	h.run(t, "var", "XYZ", "backend", "A=2", "B=3", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local", "--dry-run"); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Errorf("output = %q, want both the changed and the added key", out)
	}
}

func TestUpDryRunOnProdDoesNotPrompt(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called during a dry run")
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "production")

	if err := h.run(t, "up", "XYZ", "production", "--dry-run"); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestUpWarns|TestUpDryRun'`
Expected: FAIL — unknown flag `--dry-run`.

- [ ] **Step 3: Extend `up`**

In `internal/cli/up.go`, add the flag and rework the loop. Replace the flag block and the body between the workflow lookup and `TouchGroup`:

```go
	var (
		yes    bool
		dryRun bool
	)
	...
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing anything")
```

Skip the prod prompt when dry-running:

```go
			if wf.Tag == prodTag && !yes && !dryRun {
```

And replace the per-repo loop:

```go
			failed := 0
			for _, repo := range repos {
				report, err := inspectRepo(app, g, wf, repo)
				if err != nil {
					failed++
					fmt.Fprintln(app.Out, style.Row(style.Fail, repo.Name, err.Error()))
					continue
				}

				if dryRun {
					reportDryRun(app, repo, report)
					continue
				}

				// Cheap, read-only: warn about hand-edits this apply will lose.
				if report.Link == drift.Managed && !report.Diff.Empty() {
					fmt.Fprintln(app.Out, style.Row(style.Warn, repo.Name,
						fmt.Sprintf("%d local edit(s) will be overwritten — `prizm sync` first to keep them", driftCount(report))))
				}

				if err := applyRepo(app, g, wf, repo); err != nil {
					failed++
					fmt.Fprintln(app.Out, style.Row(style.Fail, repo.Name, err.Error()))
					continue
				}
				fmt.Fprintln(app.Out, style.Row(style.OK, repo.Name, "set ("+wf.Name+")"))
			}

			if dryRun {
				fmt.Fprintln(app.Out, "\n(dry run — nothing was written)")
				return nil
			}
```

Add the dry-run renderer to the same file:

```go
// reportDryRun prints what an apply would change for one repo.
func reportDryRun(app *App, repo store.Repo, report drift.Report) {
	if report.Link == drift.PathMissing {
		fmt.Fprintln(app.Out, style.Row(style.Fail, repo.Name, "path missing — run `prizm repair`"))
		return
	}
	if report.Link == drift.NoFile {
		fmt.Fprintln(app.Out, style.Row(style.Add, repo.Name, "would be created"))
		return
	}
	if report.Diff.Empty() {
		fmt.Fprintln(app.Out, style.Row(style.Same, repo.Name, "no change"))
		return
	}

	fmt.Fprintln(app.Out, style.Row(style.Change, repo.Name, ""))
	// Direction: the report compares expected against disk, so a key "removed"
	// from disk is one the apply would add back.
	for _, key := range report.Diff.Removed {
		fmt.Fprintf(app.Out, "    %s\n", style.Row(style.Add, key, ""))
	}
	for _, c := range report.Diff.Changed {
		fmt.Fprintf(app.Out, "    %s\n", style.Row(style.Change, c.Key, c.To+" → "+c.From))
	}
	for _, key := range report.Diff.Added {
		fmt.Fprintf(app.Out, "    %s\n", style.Row(style.Remove, key, ""))
	}
}
```

Add `"github.com/troglodytto/prizm/internal/drift"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): up --dry-run and a non-blocking local-edit warning"
```

---

### Task 7: `prizm sync`

**Files:**
- Create: `internal/syncplan/plan.go`, `internal/cli/sync.go`
- Modify: `internal/store/vars.go` (`DeleteRepoVar`), `internal/cli/root.go` (register)
- Test: `internal/syncplan/plan_test.go`, `internal/cli/sync_test.go`

**Interfaces:**
- Consumes: `drift.Report`, `resolve.ForRepoLayers`/`Attribute`/`SoleRef`, `store.SharedGroupRepos`, `sharedfile.Diff`.
- Produces:
  - `syncplan.Action` (`WriteOwningLayer`, `WriteSharedBag`, `DeleteFromOwningLayer`, `Ambiguous`).
  - `syncplan.Item` struct: `Key`, `From`, `To string`, `Action Action`, `Origin resolve.Origin`, `RefName string`, `Consumers []string`, `Reason string`.
  - `syncplan.Plan` struct: `Repo store.Repo`, `Workflow store.Workflow`, `Items []Item`; `(Plan).Empty() bool`.
  - `syncplan.Build(s *store.Store, wf store.Workflow, repo store.Repo, layers []resolve.Layer, d sharedfile.Diff, pin bool) (Plan, error)`
  - `store.(*Store).DeleteRepoVar(repoID int64, key string) error`
  - `prizm sync [group] [repo]` with `--yes` and `--pin`.

`Build` is pure over its inputs apart from one store read (a shared bag's member repos, needed to name the blast radius), which makes the classification table exhaustively testable without touching the filesystem.

- [ ] **Step 1: Write the failing classification test**

Create `internal/syncplan/plan_test.go`:

```go
package syncplan

import (
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

type fixture struct {
	store  *store.Store
	group  store.Group
	wf     store.Workflow
	repo   store.Repo
	auth   store.Repo
	bagID  int64
	layers []resolve.Layer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	g, _ := s.CreateGroup("XYZ")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID})

	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://old-host/app")
	s.SetSharedGroupVar(sg.ID, "SHARED_LITERAL", "shared-value")

	s.SetRepoVar(be.ID, "LOG_LEVEL", "debug")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "PORT", "8080")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "COMPOSITE", "${_PRIZM_DB_URL}?sslmode=disable")

	layers, err := resolve.ForRepoLayers(s, wf, be)
	if err != nil {
		t.Fatalf("ForRepoLayers() error = %v", err)
	}
	return fixture{store: s, group: g, wf: wf, repo: be, auth: auth, bagID: sg.ID, layers: layers}
}

func buildOne(t *testing.T, f fixture, d sharedfile.Diff, pin bool) Item {
	t.Helper()

	plan, err := Build(f.store, f.wf, f.repo, f.layers, d, pin)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("Build() = %d items, want 1", len(plan.Items))
	}
	return plan.Items[0]
}

func TestNewKeyGoesToTheRepoLayer(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{Added: []string{"BRAND_NEW"}}, false)
	if item.Action != WriteOwningLayer {
		t.Errorf("Action = %v, want WriteOwningLayer", item.Action)
	}
	if item.Origin.Kind != resolve.LayerWorkflowRepo {
		t.Errorf("Kind = %v, want LayerWorkflowRepo — a new key has no other owner", item.Origin.Kind)
	}
}

func TestEditedRepoOwnedKeyGoesToItsLayer(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "PORT", From: "8080", To: "9090"}},
	}, false)

	if item.Action != WriteOwningLayer {
		t.Errorf("Action = %v, want WriteOwningLayer", item.Action)
	}
	if item.Origin.Kind != resolve.LayerWorkflowRepo {
		t.Errorf("Kind = %v, want LayerWorkflowRepo", item.Origin.Kind)
	}
}

func TestEditedRepoSharedKeyGoesToTheRepoSharedLayer(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "LOG_LEVEL", From: "debug", To: "info"}},
	}, false)

	if item.Origin.Kind != resolve.LayerRepoShared {
		t.Errorf("Kind = %v, want LayerRepoShared", item.Origin.Kind)
	}
	if item.Action != WriteOwningLayer {
		t.Errorf("Action = %v, want WriteOwningLayer", item.Action)
	}
}

// A literal owned by a bag propagates — but the plan must name who else changes.
func TestEditedSharedLiteralNamesItsConsumers(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "SHARED_LITERAL", From: "shared-value", To: "new-value"}},
	}, false)

	if item.Action != WriteSharedBag {
		t.Fatalf("Action = %v, want WriteSharedBag", item.Action)
	}
	if len(item.Consumers) != 2 {
		t.Errorf("Consumers = %v, want both member repos named", item.Consumers)
	}
}

// The core ambiguity: an edited derived value.
func TestEditedDerivedValueIsAmbiguous(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "DB_URL", From: "postgres://old-host/app", To: "postgres://new-host/app"}},
	}, false)

	if item.Action != Ambiguous {
		t.Fatalf("Action = %v, want Ambiguous", item.Action)
	}
	if item.RefName != "_PRIZM_DB_URL" {
		t.Errorf("RefName = %q, want the referenced variable", item.RefName)
	}
	if len(item.Consumers) != 2 {
		t.Errorf("Consumers = %v, want the repos an update would affect", item.Consumers)
	}
}

func TestPinResolvesAmbiguityToTheRepoLayer(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "DB_URL", From: "postgres://old-host/app", To: "postgres://new-host/app"}},
	}, true)

	if item.Action != WriteOwningLayer {
		t.Errorf("Action = %v, want WriteOwningLayer under --pin", item.Action)
	}
	if item.Origin.Kind != resolve.LayerWorkflowRepo {
		t.Errorf("Kind = %v, want the pin written to this repo's own layer", item.Origin.Kind)
	}
}

// A composite template cannot be inverted, so --pin is the only option.
func TestCompositeTemplateHasNoRefName(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{
		Changed: []sharedfile.Change{{Key: "COMPOSITE", From: "postgres://old-host/app?sslmode=disable", To: "x"}},
	}, false)

	if item.Action != Ambiguous {
		t.Fatalf("Action = %v, want Ambiguous", item.Action)
	}
	if item.RefName != "" {
		t.Errorf("RefName = %q, want empty — a composite template cannot be inverted", item.RefName)
	}
	if item.Reason == "" {
		t.Error("Reason is empty; the user needs to be told why this cannot be resolved")
	}
}

func TestDeletedRepoOwnedKeyIsADelete(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{Removed: []string{"PORT"}}, false)
	if item.Action != DeleteFromOwningLayer {
		t.Errorf("Action = %v, want DeleteFromOwningLayer", item.Action)
	}
}

// Deleting a bag-owned key from one repo's file is not expressible as a delete.
func TestDeletedSharedKeyIsAmbiguous(t *testing.T) {
	f := newFixture(t)

	item := buildOne(t, f, sharedfile.Diff{Removed: []string{"SHARED_LITERAL"}}, false)
	if item.Action != Ambiguous {
		t.Errorf("Action = %v, want Ambiguous", item.Action)
	}
	if item.Reason == "" {
		t.Error("Reason is empty; the user needs the two real options spelled out")
	}
}

func TestEmptyDiffMakesAnEmptyPlan(t *testing.T) {
	f := newFixture(t)

	plan, err := Build(f.store, f.wf, f.repo, f.layers, sharedfile.Diff{}, false)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !plan.Empty() {
		t.Errorf("Plan = %+v, want empty", plan)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/syncplan/`
Expected: FAIL — `undefined: Build`.

- [ ] **Step 3: Implement the planner**

Create `internal/syncplan/plan.go`:

```go
// Package syncplan classifies hand-edits found in a repo's env file into
// actions that can be explained before they are applied.
package syncplan

import (
	"fmt"
	"strings"

	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

// Action is what sync would do about one edited key.
type Action int

const (
	// WriteOwningLayer writes the new value to the layer that defined the key,
	// or to the repo+workflow layer when the key is new.
	WriteOwningLayer Action = iota
	// WriteSharedBag writes to a shared bag, changing every repo that uses it.
	WriteSharedBag
	// DeleteFromOwningLayer removes a key its own layer defined.
	DeleteFromOwningLayer
	// Ambiguous means prizm will not guess. Skipped unless the user chooses.
	Ambiguous
)

// Item is one classified edit.
type Item struct {
	Key       string
	From      string
	To        string
	Action    Action
	Origin    resolve.Origin
	RefName   string   // set when the template is exactly ${RefName}
	Consumers []string // repos affected if a shared bag is written
	Reason    string   // why this is ambiguous, in the user's terms
}

// Plan is everything sync would do for one repo.
type Plan struct {
	Repo     store.Repo
	Workflow store.Workflow
	Items    []Item
}

// Empty reports whether there is nothing to do.
func (p Plan) Empty() bool { return len(p.Items) == 0 }

// Build classifies a drift diff. pin forces derived-value ambiguities to
// resolve as a literal on this repo's own layer.
func Build(s *store.Store, wf store.Workflow, repo store.Repo, layers []resolve.Layer, d sharedfile.Diff, pin bool) (Plan, error) {
	plan := Plan{Repo: repo, Workflow: wf}

	// Keys the human added to the file: nothing else can own them.
	for _, key := range d.Added {
		plan.Items = append(plan.Items, Item{
			Key:    key,
			To:     "",
			Action: WriteOwningLayer,
			Origin: resolve.Origin{Layer: wf.Name + "+" + repo.Name, Kind: resolve.LayerWorkflowRepo},
		})
	}

	for _, change := range d.Changed {
		item, err := classifyChange(s, wf, repo, layers, change, pin)
		if err != nil {
			return Plan{}, err
		}
		plan.Items = append(plan.Items, item)
	}

	for _, key := range d.Removed {
		item, err := classifyRemoval(s, layers, key)
		if err != nil {
			return Plan{}, err
		}
		plan.Items = append(plan.Items, item)
	}

	return plan, nil
}

func classifyChange(s *store.Store, wf store.Workflow, repo store.Repo, layers []resolve.Layer, c sharedfile.Change, pin bool) (Item, error) {
	item := Item{Key: c.Key, From: c.From, To: c.To}

	origin, ok := resolve.Attribute(layers, c.Key)
	if !ok {
		item.Action = WriteOwningLayer
		item.Origin = resolve.Origin{Layer: wf.Name + "+" + repo.Name, Kind: resolve.LayerWorkflowRepo}
		return item, nil
	}
	item.Origin = origin

	// A derived value: the human edited an expansion, not the template.
	if strings.Contains(origin.Template, "${") {
		ref, invertible := resolve.SoleRef(origin.Template)

		if pin {
			item.Action = WriteOwningLayer
			item.Origin = resolve.Origin{Layer: wf.Name + "+" + repo.Name, Kind: resolve.LayerWorkflowRepo}
			return item, nil
		}

		item.Action = Ambiguous
		if invertible {
			item.RefName = ref
			consumers, err := refConsumers(s, layers, ref)
			if err != nil {
				return Item{}, err
			}
			item.Consumers = consumers
			item.Reason = fmt.Sprintf(
				"comes from ${%s} in %s — update the shared value (also changes %s), or pin this literal on %s only",
				ref, origin.Layer, strings.Join(consumers, ", "), repo.Name)
		} else {
			item.Reason = fmt.Sprintf(
				"built from the template %q in %s, which prizm cannot invert — edit that template, or re-run with --pin to set a literal on %s only",
				origin.Template, origin.Layer, repo.Name)
		}
		return item, nil
	}

	// A literal. Where it lives decides the blast radius.
	if origin.Kind == resolve.LayerSharedGroup {
		consumers, err := repoNames(s, origin.SharedGroupID)
		if err != nil {
			return Item{}, err
		}
		item.Action = WriteSharedBag
		item.Consumers = consumers
		return item, nil
	}

	item.Action = WriteOwningLayer
	return item, nil
}

func classifyRemoval(s *store.Store, layers []resolve.Layer, key string) (Item, error) {
	item := Item{Key: key}

	origin, ok := resolve.Attribute(layers, key)
	if !ok {
		// Already gone from every layer; nothing to do.
		item.Action = Ambiguous
		item.Reason = "not defined in any layer — nothing to remove"
		return item, nil
	}
	item.Origin = origin

	if origin.Kind == resolve.LayerSharedGroup {
		consumers, err := repoNames(s, origin.SharedGroupID)
		if err != nil {
			return Item{}, err
		}
		item.Action = Ambiguous
		item.Consumers = consumers
		item.Reason = fmt.Sprintf(
			"defined in %s, which also feeds %s — delete it from the bag file, or remove this repo from the bag",
			origin.Layer, strings.Join(consumers, ", "))
		return item, nil
	}

	item.Action = DeleteFromOwningLayer
	return item, nil
}

// refConsumers names the repos affected by changing the variable a template
// references, when that variable lives in a shared bag.
func refConsumers(s *store.Store, layers []resolve.Layer, ref string) ([]string, error) {
	origin, ok := resolve.Attribute(layers, ref)
	if !ok || origin.Kind != resolve.LayerSharedGroup {
		return nil, nil
	}
	return repoNames(s, origin.SharedGroupID)
}

func repoNames(s *store.Store, sharedGroupID int64) ([]string, error) {
	repos, err := s.SharedGroupRepos(sharedGroupID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/syncplan/ -v`
Expected: PASS — all eleven classification tests.

- [ ] **Step 5: Write the failing command test**

Create `internal/cli/sync_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editApplied rewrites the file the repo's symlink points at.
func editApplied(t *testing.T, repoDir, content string) {
	t.Helper()

	dest, err := os.Readlink(filepath.Join(repoDir, ".env"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if err := os.WriteFile(dest, []byte(content), 0o600); err != nil {
		t.Fatalf("writing edit: %v", err)
	}
}

func TestSyncWritesAnEditBackToTheRepoLayer(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)

	editApplied(t, beDir, "A=EDITED\n")

	if err := h.run(t, "sync", "XYZ", "backend"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if got["A"] != "EDITED" {
		t.Errorf("A = %q, want the edited value written back", got["A"])
	}
}

func TestSyncRegeneratesTheFileAfterwards(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)

	// Deliberately messy: unsorted, extra blank lines.
	editApplied(t, beDir, "\n\nA=EDITED\n\n")
	h.run(t, "sync", "XYZ", "backend")

	body, _ := os.ReadFile(filepath.Join(beDir, ".env"))
	if string(body) != "A=EDITED\n" {
		t.Errorf("file = %q, want it regenerated from prizm after sync", body)
	}
}

func TestSyncRecordsTheSourceAsSync(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)

	editApplied(t, beDir, "A=EDITED\n")
	h.run(t, "sync", "XYZ", "backend")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")

	snaps, _ := h.app.Store.ListSnapshots(store.WorkflowRepoScope(wf.ID, repo.ID))
	if len(snaps) == 0 || snaps[0].Source != "sync" {
		t.Errorf("newest snapshot = %+v, want one tagged sync", snaps)
	}
}

func TestSyncRespectsDeclining(t *testing.T) {
	h := newHarness(t)
	beDir := h.seedApplied(t)

	h.app.Confirm = func(string) (bool, error) { return false, nil }
	editApplied(t, beDir, "A=EDITED\n")

	if err := h.run(t, "sync", "XYZ", "backend"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)

	if got["A"] != "1" {
		t.Errorf("A = %q, want the original value after declining", got["A"])
	}
}

func TestSyncSkipsAmbiguousDerivedValues(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.AddSharedGroupRepo(sg.ID, repo.ID)
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://old/app")
	h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")

	editApplied(t, beDir, "DB_URL=postgres://new/app\n")

	if err := h.run(t, "sync", "XYZ", "backend"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "_PRIZM_DB_URL") {
		t.Errorf("output = %q, want it to name the referenced variable", out)
	}
	if !strings.Contains(out, "--pin") {
		t.Errorf("output = %q, want it to offer --pin", out)
	}

	vars, _ := h.app.Store.SharedGroupVars(sg.ID)
	if vars["_PRIZM_DB_URL"] != "postgres://old/app" {
		t.Errorf("shared value = %q, want it untouched — ambiguity must not be guessed", vars["_PRIZM_DB_URL"])
	}
}

func TestSyncPinWritesALiteralToTheRepoLayer(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.AddSharedGroupRepo(sg.ID, repo.ID)
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://old/app")
	h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")

	editApplied(t, beDir, "DB_URL=postgres://new/app\n")

	if err := h.run(t, "sync", "XYZ", "backend", "--pin"); err != nil {
		t.Fatalf("sync --pin error = %v", err)
	}

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if got["DB_URL"] != "postgres://new/app" {
		t.Errorf("DB_URL = %q, want the pinned literal", got["DB_URL"])
	}
	vars, _ := h.app.Store.SharedGroupVars(sg.ID)
	if vars["_PRIZM_DB_URL"] != "postgres://old/app" {
		t.Errorf("shared value = %q, want it untouched under --pin", vars["_PRIZM_DB_URL"])
	}
}

func TestSyncCleanRepoIsQuiet(t *testing.T) {
	h := newHarness(t)
	h.seedApplied(t)

	if err := h.run(t, "sync", "XYZ", "backend"); err != nil {
		t.Fatalf("sync error = %v", err)
	}
	if !strings.Contains(h.out.String(), "up to date") {
		t.Errorf("output = %q, want an up-to-date message", h.out.String())
	}
}
```

Add `"github.com/troglodytto/prizm/internal/store"` to that file's imports.

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestSync`
Expected: FAIL — unknown command `sync`.

- [ ] **Step 7: Implement the command**

Add the missing delete to `internal/store/vars.go`:

```go
// DeleteRepoVar removes a key from the repo-shared layer.
func (s *Store) DeleteRepoVar(repoID int64, key string) error {
	_, err := s.db.Exec(`DELETE FROM repo_vars WHERE repo_id = ? AND key = ?`, repoID, key)
	return err
}
```

Create `internal/cli/sync.go`:

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/drift"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/syncplan"
)

func newSyncCmd(app *App) *cobra.Command {
	var (
		yes bool
		pin bool
	)

	cmd := &cobra.Command{
		Use:   "sync [group] [repo]",
		Short: "Reconcile hand-edits in a repo's env file back into prizm",
		Long: "The file is the source of truth for this command. Edits are attributed\n" +
			"to the layer that defined each key, and anything ambiguous is reported\n" +
			"rather than guessed.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repos, err := syncTargets(app, args)
			if err != nil {
				return err
			}

			applied, err := app.Store.AppliedFor(g.ID)
			if err != nil {
				return err
			}

			for _, repo := range repos {
				state, ok := applied[repo.ID]
				if !ok {
					fmt.Fprintf(app.Out, "%s not applied — nothing to sync\n", repo.Name)
					continue
				}
				if err := syncRepo(app, g, state.WorkflowID, repo, yes, pin); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "apply without confirmation")
	cmd.Flags().BoolVar(&pin, "pin", false, "resolve derived-value edits as a literal on this repo only")
	return cmd
}

// syncTargets resolves which repos to sync from optional group/repo args.
func syncTargets(app *App, args []string) (store.Group, []store.Repo, error) {
	if len(args) == 2 {
		g, repo, _, err := app.splitGroupRepo(args, 0)
		if err != nil {
			return store.Group{}, nil, err
		}
		return g, []store.Repo{repo}, nil
	}

	g, rest, err := app.splitGroup(args, 0)
	if err != nil {
		return store.Group{}, nil, err
	}
	if len(rest) == 1 {
		repo, err := app.Store.RepoByName(g.ID, rest[0])
		if err != nil {
			return store.Group{}, nil, fmt.Errorf("no such repo %q in group %s", rest[0], g.Name)
		}
		return g, []store.Repo{repo}, nil
	}

	repos, err := app.Store.ListRepos(g.ID)
	return g, repos, err
}

func syncRepo(app *App, g store.Group, workflowID int64, repo store.Repo, yes, pin bool) error {
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return err
	}
	var wf store.Workflow
	for _, w := range workflows {
		if w.ID == workflowID {
			wf = w
		}
	}
	if wf.ID == 0 {
		return fmt.Errorf("%s is linked to a workflow that no longer exists", repo.Name)
	}

	report, err := inspectRepo(app, g, wf, repo)
	if err != nil {
		return err
	}
	if report.Link == drift.PathMissing {
		fmt.Fprintf(app.Out, "%s path missing — run `prizm repair`\n", repo.Name)
		return nil
	}
	if report.Diff.Empty() {
		fmt.Fprintf(app.Out, "%s up to date\n", repo.Name)
		return nil
	}

	layers, err := resolve.ForRepoLayers(app.Store, wf, repo)
	if err != nil {
		return err
	}
	plan, err := syncplan.Build(app.Store, wf, repo, layers, report.Diff, pin)
	if err != nil {
		return err
	}

	fmt.Fprintf(app.Out, "%s/%s ← %s/%s\n", g.Name, repo.Name, repo.Path, repo.EnvFile)
	actionable := renderPlan(app, plan)
	if actionable == 0 {
		fmt.Fprintf(app.Out, "  nothing prizm can apply without a decision from you\n")
		return nil
	}

	if !yes {
		ok, err := app.Confirm("Apply? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(app.Out, "%s skipped\n", repo.Name)
			return nil
		}
	}

	if err := applyPlan(app, wf, repo, plan); err != nil {
		return err
	}

	// Regenerate the file so it matches prizm exactly again.
	if err := applyRepo(app, g, wf, repo); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "%s synced\n", repo.Name)
	return nil
}

// renderPlan prints the plan and returns how many items can be applied.
func renderPlan(app *App, plan syncplan.Plan) int {
	actionable := 0

	for _, item := range plan.Items {
		switch item.Action {
		case syncplan.WriteOwningLayer:
			actionable++
			fmt.Fprintf(app.Out, "  %s\n", style.Row(style.Change, item.Key, "→ "+item.Origin.Layer))
		case syncplan.WriteSharedBag:
			actionable++
			fmt.Fprintf(app.Out, "  %s\n", style.Row(style.Change, item.Key,
				fmt.Sprintf("→ %s  (also changes: %s)", item.Origin.Layer, strings.Join(item.Consumers, ", "))))
		case syncplan.DeleteFromOwningLayer:
			actionable++
			fmt.Fprintf(app.Out, "  %s\n", style.Row(style.Remove, item.Key, "from "+item.Origin.Layer))
		case syncplan.Ambiguous:
			fmt.Fprintf(app.Out, "  %s\n      %s\n", style.Row(style.Ask, item.Key, "skipped"), style.Detail(item.Reason))
			if item.RefName != "" {
				fmt.Fprintf(app.Out, "      edit %s in the bag file, or re-run with --pin\n", item.RefName)
			} else {
				fmt.Fprintf(app.Out, "      re-run with --pin to set a literal on %s only\n", plan.Repo.Name)
			}
		}
	}
	return actionable
}

func applyPlan(app *App, wf store.Workflow, repo store.Repo, plan syncplan.Plan) error {
	for _, item := range plan.Items {
		switch item.Action {
		case syncplan.WriteOwningLayer:
			var err error
			if item.Origin.Kind == resolve.LayerRepoShared {
				err = app.Store.SetRepoVar(repo.ID, item.Key, item.To)
			} else {
				err = app.Store.SetWorkflowRepoVarWithSource(wf.ID, repo.ID, item.Key, item.To, store.SourceSync)
			}
			if err != nil {
				return err
			}

		case syncplan.WriteSharedBag:
			vars, err := app.Store.SharedGroupVars(item.Origin.SharedGroupID)
			if err != nil {
				return err
			}
			vars[item.Key] = item.To
			if err := app.Store.ReplaceSharedGroupVarsWithSource(
				item.Origin.SharedGroupID, vars, store.SourceSync,
				"reconciled from "+repo.Name,
			); err != nil {
				return err
			}
			// The bag is file-backed: keep its file in step, or the next
			// `shared-sync` would revert this write.
			bag, err := app.Store.SharedGroupByID(item.Origin.SharedGroupID)
			if err != nil {
				return err
			}
			if bag.FilePath != "" {
				if err := writeBagFile(app, bag.ID, bag.FilePath); err != nil {
					return err
				}
			}

		case syncplan.DeleteFromOwningLayer:
			var err error
			if item.Origin.Kind == resolve.LayerRepoShared {
				err = app.Store.DeleteRepoVar(repo.ID, item.Key)
			} else {
				err = app.Store.DeleteWorkflowRepoVar(wf.ID, repo.ID, item.Key, store.SourceSync)
			}
			if err != nil {
				return err
			}

		case syncplan.Ambiguous:
			// Deliberately nothing.
		}
	}
	return nil
}
```

Add the lookup the plan applier needs, in `internal/store/sharedfiles.go`:

```go
// SharedGroupByID looks a bag up by its primary key.
func (s *Store) SharedGroupByID(id int64) (SharedGroup, error) {
	var sg SharedGroup
	err := s.db.QueryRow(
		`SELECT id, workflow_id, name, file_path FROM shared_groups WHERE id = ?`, id,
	).Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath)

	if errors.Is(err, sql.ErrNoRows) {
		return SharedGroup{}, fmt.Errorf("shared group %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return SharedGroup{}, err
	}
	return sg, nil
}
```

Add `"database/sql"`, `"errors"` and `"fmt"` to that file's imports, and register the command in `internal/cli/root.go`: `newSyncCmd(app),`.

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./... `
Expected: PASS across every package.

- [ ] **Step 9: Commit**

```bash
git add internal/syncplan/ internal/cli/ internal/store/
git commit -m "feat(sync): attribute hand-edits to their layer and reconcile them"
```

---

### Task 8: `prizm repair` and the apply lock

**Files:**
- Create: `internal/cli/repair.go`, `internal/lock/lock.go`, `internal/lock/lock_unix.go`, `internal/lock/lock_other.go`
- Modify: `internal/store/repos.go` (`UpdateRepoPath`), `internal/cli/up.go` and `internal/cli/sync.go` (take the lock), `internal/cli/root.go` (register)
- Test: `internal/cli/repair_test.go`, `internal/lock/lock_test.go`

**Interfaces:**
- Produces:
  - `store.(*Store).UpdateRepoPath(repoID int64, path string) error`
  - `lock.Acquire(path string) (*Lock, error)` and `(*Lock).Release() error` — advisory whole-file lock; `lock.ErrBusy` when another prizm holds it.
  - `prizm repair [group] <repo> [--path P]` — re-point a repo whose checkout moved. Defaults to the current directory.

The spec named `repair` explicitly as the escape hatch for the one thing that breaks the path contract. The lock closes the footgun the spec also named: two `up`s racing in two terminals, last-write-wins on a symlink.

- [ ] **Step 1: Write the failing lock test**

Create `internal/lock/lock_test.go`:

```go
package lock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apply.lock")

	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("Release() error = %v", err)
	}
}

func TestAcquireTwiceInTheSameProcessIsBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apply.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer first.Release()

	if _, err := Acquire(path); !errors.Is(err, ErrBusy) {
		t.Errorf("second Acquire() error = %v, want ErrBusy", err)
	}
}

func TestAcquireAfterReleaseSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apply.lock")

	first, _ := Acquire(path)
	first.Release()

	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() after Release() error = %v", err)
	}
	second.Release()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lock/`
Expected: FAIL — `undefined: Acquire`.

- [ ] **Step 3: Implement the lock**

Create `internal/lock/lock.go`:

```go
// Package lock provides an advisory file lock so two prizm applies cannot race
// on the same repos.
package lock

import (
	"errors"
	"os"
)

// ErrBusy means another prizm process holds the lock.
var ErrBusy = errors.New("another prizm command is applying changes")

// Lock is a held advisory lock.
type Lock struct {
	file *os.File
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlock(l.file)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}
```

Create `internal/lock/lock_unix.go`:

```go
//go:build unix

package lock

import (
	"fmt"
	"os"
	"syscall"
)

// Acquire takes an exclusive, non-blocking lock on path.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return &Lock{file: f}, nil
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
```

Create `internal/lock/lock_other.go`:

```go
//go:build !unix

package lock

import (
	"fmt"
	"os"
)

// Acquire is a no-op on platforms without flock. prizm still works; two
// simultaneous applies are simply not prevented.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	return &Lock{file: f}, nil
}

func unlock(*os.File) error { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lock/ -v`
Expected: PASS.

- [ ] **Step 5: Take the lock in the writing commands**

Add to `internal/cli/root.go`:

```go
// withApplyLock runs fn while holding prizm's advisory apply lock.
func (a *App) withApplyLock(fn func() error) error {
	dir, err := config.DataDir()
	if err != nil {
		return err
	}
	if err := config.EnsureDir(dir); err != nil {
		return err
	}

	l, err := lock.Acquire(filepath.Join(dir, "apply.lock"))
	if err != nil {
		return err
	}
	defer l.Release()

	return fn()
}
```

Add `"github.com/troglodytto/prizm/internal/lock"` to its imports. Then wrap the body of `up`'s `RunE` (everything after the prod confirmation, and only when `!dryRun`) and `sync`'s `RunE` in `app.withApplyLock(func() error { ... })`.

- [ ] **Step 6: Write the failing repair test**

Create `internal/cli/repair_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairRepointsARepo(t *testing.T) {
	h := newHarness(t)
	oldDir := h.repoDir(t, "backend")
	newDir := h.repoDir(t, "backend-moved")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", oldDir)
	os.RemoveAll(oldDir)

	if err := h.run(t, "repair", "XYZ", "backend", "--path", newDir); err != nil {
		t.Fatalf("repair error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	if repo.Path != newDir {
		t.Errorf("Path = %q, want %q", repo.Path, newDir)
	}
}

func TestRepairDefaultsToCwd(t *testing.T) {
	h := newHarness(t)
	oldDir := h.repoDir(t, "backend")
	newDir := h.repoDir(t, "backend-moved")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", oldDir)
	h.cwd = newDir

	if err := h.run(t, "repair", "XYZ", "backend"); err != nil {
		t.Fatalf("repair error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	if repo.Path != newDir {
		t.Errorf("Path = %q, want cwd %q", repo.Path, newDir)
	}
}

func TestRepairRejectsAMissingTarget(t *testing.T) {
	h := newHarness(t)
	oldDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", oldDir)

	err := h.run(t, "repair", "XYZ", "backend", "--path", filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("repair to a missing directory error = nil, want error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %q, want it to name the bad path", err)
	}
}

func TestRepairTellsYouToReapply(t *testing.T) {
	h := newHarness(t)
	oldDir := h.repoDir(t, "backend")
	newDir := h.repoDir(t, "backend-moved")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", oldDir)
	h.run(t, "repair", "XYZ", "backend", "--path", newDir)

	if !strings.Contains(h.out.String(), "up") {
		t.Errorf("output = %q, want it to point at re-applying", h.out.String())
	}
}
```

- [ ] **Step 7: Implement repair**

Add to `internal/store/repos.go`:

```go
// UpdateRepoPath re-points a repo whose checkout moved. This is the only
// command that may change a path.
func (s *Store) UpdateRepoPath(repoID int64, path string) error {
	_, err := s.db.Exec(`UPDATE repos SET path = ? WHERE id = ?`, path, repoID)
	return err
}
```

Create `internal/cli/repair.go`:

```go
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRepairCmd(app *App) *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "repair [group] <repo>",
		Short: "Re-point a repo whose checkout moved",
		Long: "Repo paths are a stable contract; moving a checkout breaks it. This is\n" +
			"the one command that changes a path. Defaults to the current directory.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repo, _, err := app.splitGroupRepo(args, 0)
			if err != nil {
				return err
			}

			abs, err := resolvePath(app, path)
			if err != nil {
				return err
			}
			if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
				return fmt.Errorf("%s is not an existing directory", abs)
			}

			if err := app.Store.UpdateRepoPath(repo.ID, abs); err != nil {
				return err
			}

			fmt.Fprintf(app.Out, "%s/%s → %s\n  run `prizm up` to re-link it\n", g.Name, repo.Name, abs)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "new repo path (default: current directory)")
	return cmd
}
```

Register it: `newRepairCmd(app),`.

- [ ] **Step 8: Run the full suite**

Run: `go test ./... && go build ./...`
Expected: PASS and a clean build.

- [ ] **Step 9: Commit**

```bash
git add internal/lock/ internal/cli/ internal/store/
git commit -m "feat(cli): repair for moved repos and an advisory apply lock"
```

---

## Phase 2 Self-Review

**Spec coverage.** `prizm status` (spec: "the one you end up wanting almost immediately") → Task 5. `prizm sync` with per-key comparison and no mtime → Tasks 4, 7. Warning on `up` that points at `sync` and never blocks → Task 6. Dry run → Task 6. Snapshot history with source tagging, guarded against no-op spam → Tasks 1, 2. `repair` → Task 8. The concurrency footgun the spec flagged as "not worth solving now" → Task 8, solved cheaply.

**Placeholder scan.** No task defers work to prose. Every step carries runnable code.

**Type consistency.** `resolve.Layer` gains two fields in Task 3; Phase 1's construction sites are all inside `ForRepoLayers`, which Task 3 rewrites wholesale, so nothing else needs touching. `sharedfile.Diff` is reused rather than redefined, keeping one diff type across `shared-sync`, `drift` and `syncplan`. `store.SourceSync` and friends are defined once in Task 1 and referenced by Tasks 2 and 7. `inspectRepo` and `driftCount` are defined in Task 5 and reused by Tasks 6 and 7.

**Watch during execution:**

- Task 6 renders the drift diff **reversed** — `drift.Inspect` compares expected-against-disk, so from `up`'s point of view a key "removed" from disk is one it would add. Getting this backwards produces a confidently wrong dry run.
- Task 7's `applyPlan` must rewrite the bag's file after writing a shared value. Skipping that leaves the file and DB disagreeing, and the next `shared-sync` silently reverts the sync.
