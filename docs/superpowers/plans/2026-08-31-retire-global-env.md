# Retire global.env Implementation Plan

**Goal:** Make the database the sole source of truth for the group-global variable layer, so a bag `shared-sync` can no longer silently delete every group-global variable.

**Architecture:** The group layer is currently backed by a file (`shared/<group>/global.env`) that only `sync` keeps in step — `var --global`, `unset --global` and `edit --global` all leave it stale, and `shared-sync` then reconciles the stale file over the database. Rather than add the missing writes in three more places, delete the file layer: point `prizm global` at the same database-backed `$EDITOR` round-trip `prizm edit --global` already uses, stop `shared-sync` touching the group layer, then remove the now-dead file machinery.

**Tech Stack:** Go, cobra, SQLite (`internal/store`), existing `harness` test helpers in `internal/cli`.

**Spec:** `docs/superpowers/specs/2026-08-31-cross-layer-editor-design.md` — section "Prerequisite: retire `global.env`"

## Global Constraints

- Every commit must pass `go vet ./...` and `go test -race ./...` — these are the CI gates.
- Bag files keep their file-is-source-of-truth behaviour. This plan changes the **group** layer only; `shared-add`, `shared-edit`, `shared-ls` and bag syncing are untouched.
- Existing `global.env` files on users' disks become inert. Do not delete them — deleting a user's file to complete a refactor is not this change's business.
- No new dependencies.

---

## The bug this fixes

Reproduced against a real group before this plan was written:

```
prizm var kroolo --global _PRIZM_CANARY=…
  → group_vars=1, global.env unchanged at 110 bytes, canary absent from the file
prizm shared-sync kroolo workos-qa ports --yes     # scoped to ONE bag
  → "✓ kroolo (global)  synced"
  → group_vars=0        ← every group-global variable destroyed
```

Two independent gaps combine:

1. `internal/cli/global.go:172` defines `rewriteGlobalFile`, whose comment says it exists "so the file and the database do not disagree". **It is never called** — dead code.
2. `internal/cli/shared.go:185` calls `syncAllGlobals` on every `shared-sync`, including one scoped to a single bag.

`internal/cli/sync.go:385` (`writeSharedValue`) is the one path that *does* patch the file, and its comment names the exact failure: "Without that the next `shared-sync` would read the stale file and quietly revert this."

## File Structure

| File | Responsibility after this plan |
| --- | --- |
| `internal/cli/global.go` | `prizm global` only — a database-backed `$EDITOR` round-trip. All file machinery removed. |
| `internal/cli/shared.go` | Bag reconciliation only. No group-layer awareness. |
| `internal/cli/sync.go` | `writeSharedValue` writes group vars to the database; keeps patching **bag** files. |
| `internal/config/paths.go` | Loses `GlobalPath`. Bag and built paths unchanged. |
| `internal/cli/global_test.go` | **New.** Owns both regressions: `global` writes to the DB, and a bag sync leaves the group layer alone. |

---

### Task 1: Point `prizm global` at the database

`prizm global` currently materialises `global.env`, shells out to `$EDITOR` directly, and tells you to run `shared-sync`. That round-trip is the reason the file exists. `editScope` in `internal/cli/edit.go:50` already does the whole job against the database — temp file, parse, replace, snapshot — so `global` becomes a thin caller of it.

**Files:**
- Create: `internal/cli/global_test.go`
- Modify: `internal/cli/global.go:16-55` (the `newGlobalCmd` body)

**Interfaces:**
- Consumes: `editScope(app *App, scope store.Scope, label string) error` from `internal/cli/edit.go:50`; `store.GroupScope(groupID int64) store.Scope`; `(*App).splitGroup(args []string, n int) (store.Group, []string, error)`
- Produces: nothing new. `prizm global <group>` gains database-backed behaviour; `newGlobalCmd` keeps its signature.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/global_test.go`:

```go
package cli

import "testing"

func TestGlobalWritesStraightToTheDatabase(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	// editWith stubs $EDITOR. The old implementation shelled out to
	// exec.Command instead, so it never saw this and wrote nothing.
	h.editWith(func(string) string { return "REGION=us-east-2\n" })

	if err := h.run(t, "global", "XYZ"); err != nil {
		t.Fatalf("global: %v", err)
	}

	g, err := h.app.Store.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName: %v", err)
	}
	vars, err := h.app.Store.GroupVars(g.ID)
	if err != nil {
		t.Fatalf("GroupVars: %v", err)
	}
	if vars["REGION"] != "us-east-2" {
		t.Errorf("REGION = %q, want %q — `global` must land in the database with no shared-sync",
			vars["REGION"], "us-east-2")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestGlobalWritesStraightToTheDatabase -v`

Expected: FAIL. `REGION = "" ...` — the old command builds `exec.Command(editor, path)` and ignores `app.EditFile`, so the stub never runs and nothing reaches the database.

- [ ] **Step 3: Rewrite `newGlobalCmd`**

Replace the whole of `newGlobalCmd` in `internal/cli/global.go` (lines 16-55) with:

```go
func newGlobalCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "global [group]",
		ValidArgsFunction: positions(app, compGroup),
		Short:             "Edit the variables shared across every workflow in a group",
		Long: "Group-global variables are facts about the whole group — a shared\n" +
			"database cluster's username, an AWS account — true in every workflow\n" +
			"and every repo.\n\n" +
			"They are the lowest layer, so they are defaults rather than bindings:\n" +
			"when a value stops being universal, any layer above simply overrides\n" +
			"it and nothing has to be unwired first.\n\n" +
			"What you save is applied immediately. This is the same layer as\n" +
			"`prizm edit --global`.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}
			return editScope(app, store.GroupScope(g.ID), g.Name+" (global)")
		},
	}
}
```

Then delete the now-unused imports from `internal/cli/global.go` — `fmt`, `os`, `os/exec`, and `github.com/troglodytto/prizm/internal/sharedfile` are no longer needed by this function. Leave `config`, `store` and `style` for now; Task 3 removes what remains.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestGlobalWritesStraightToTheDatabase -v`

Expected: PASS.

- [ ] **Step 5: Run the whole suite**

Run: `go vet ./... && go test -race ./...`

Expected: PASS. If `go vet` reports unused imports in `global.go`, remove exactly those it names.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/global.go internal/cli/global_test.go
git commit -m "fix: prizm global writes to the database, not a file

The file round-trip was the only reason the group layer needed
shared-sync. editScope already does this against the database, with a
snapshot, so global becomes a caller of it."
```

---

### Task 2: Stop `shared-sync` reconciling the group layer

With Task 1 landed, nothing needs `shared-sync` to read `global.env` — so the call that destroys group variables can go. This is the actual bug fix.

**Files:**
- Modify: `internal/cli/shared.go:185` (remove the `syncAllGlobals` call)
- Modify: `internal/cli/global_test.go` (add the regression test)

**Interfaces:**
- Consumes: `(*harness).sharedFixture(t) (beDir, authDir, file string)` from `internal/cli/shared_test.go:12`, which creates group `XYZ`, workflow `local`, and bag `db`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/global_test.go`:

```go
// A bag sync must not touch the group layer. It used to: shared-sync called
// syncAllGlobals, which reconciled a global.env that no direct edit path ever
// rewrote — so the stale file's silence deleted every group variable.
func TestBagSyncLeavesGroupGlobalsAlone(t *testing.T) {
	h := newHarness(t)
	h.sharedFixture(t)

	if err := h.run(t, "var", "XYZ", "--global", "REGION=us-east-2"); err != nil {
		t.Fatalf("var --global: %v", err)
	}

	if err := h.run(t, "shared-sync", "XYZ", "local", "db", "--yes"); err != nil {
		t.Fatalf("shared-sync: %v", err)
	}

	g, err := h.app.Store.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName: %v", err)
	}
	vars, err := h.app.Store.GroupVars(g.ID)
	if err != nil {
		t.Fatalf("GroupVars: %v", err)
	}
	if vars["REGION"] != "us-east-2" {
		t.Errorf("REGION = %q after a bag sync, want %q — syncing one bag must not "+
			"reconcile the group layer", vars["REGION"], "us-east-2")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestBagSyncLeavesGroupGlobalsAlone -v`

Expected: FAIL. `REGION = "" after a bag sync, want "us-east-2"` — `syncAllGlobals` read a `global.env` that has no `REGION` in it and replaced the group layer with the file's contents.

- [ ] **Step 3: Remove the call**

In `internal/cli/shared.go`, delete these four lines from the `RunE` of `newSharedSyncCmd` (at line 185):

```go
			if err := syncAllGlobals(app, args, yes); err != nil {
				return err
			}

```

so the body now opens directly with `bags, err := selectBags(app, args)`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestBagSyncLeavesGroupGlobalsAlone -v`

Expected: PASS.

- [ ] **Step 5: Run the whole suite**

Run: `go vet ./... && go test -race ./...`

Expected: PASS, except `go vet` may now report `syncAllGlobals` as unused. That is expected — Task 3 deletes it. If the build fails on it, move Task 3's deletion of `syncAllGlobals` forward into this commit.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/shared.go internal/cli/global_test.go
git commit -m "fix: syncing one bag no longer wipes the group layer

shared-sync called syncAllGlobals unconditionally, reconciling a
global.env that var/unset/edit --global never rewrote. Syncing a single
bag deleted every group-global variable."
```

---

### Task 3: Delete the file machinery

Everything backing `global.env` is now unreachable. Removing it is what stops the desync coming back — a future write path cannot forget to call a function that does not exist.

**Files:**
- Modify: `internal/cli/global.go` — delete `ensureGlobalFile`, `syncGlobal`, `filepathDir`, `groupFilePath`, `rewriteGlobalFile`
- Modify: `internal/cli/shared.go` — delete `syncAllGlobals` (lines 347-368)
- Modify: `internal/cli/sync.go:385-394` — `writeSharedValue` stops patching the group file
- Modify: `internal/config/paths.go:47-54` — delete `GlobalPath`

**Interfaces:**
- Consumes: `store.SetGroupVar(groupID int64, key, value string) error`
- Produces: `writeSharedValue(app *App, origin resolve.Origin, key, value string) error` — signature unchanged, group branch no longer touches the filesystem.

- [ ] **Step 1: Simplify `writeSharedValue`**

In `internal/cli/sync.go`, replace the group branch and its comment (lines 382-394) with:

```go
// writeSharedValue updates a group-global or shared-bag variable, keeping the
// bag's backing file in step. Without that the next `shared-sync` would read
// the stale file and quietly revert this. The group layer has no file.
func writeSharedValue(app *App, origin resolve.Origin, key, value string) error {
	if origin.Kind == resolve.LayerGroup {
		return app.Store.SetGroupVar(origin.GroupID, key, value)
	}
```

Leave the rest of the function — the bag branch still patches `bag.FilePath`, which is correct.

- [ ] **Step 2: Delete the dead functions**

- In `internal/cli/global.go`, delete `ensureGlobalFile`, `syncGlobal`, `filepathDir`, `groupFilePath` and `rewriteGlobalFile`. The file should end up containing only `newGlobalCmd` and its imports.
- In `internal/cli/shared.go`, delete `syncAllGlobals` (the comment block and function at lines 347-368).
- In `internal/config/paths.go`, delete `GlobalPath` (lines 47-54).

- [ ] **Step 3: Fix up imports**

Run: `go build ./...`

Remove exactly the imports the compiler reports as unused. Expect `internal/cli/global.go` to need only `github.com/spf13/cobra` and `github.com/troglodytto/prizm/internal/store`; `internal/config/paths.go` keeps all of its imports because `DataDir`, `DBPath`, `BuiltPath` and `EnsureDir` remain.

- [ ] **Step 4: Verify nothing references the removed symbols**

Run:

```bash
grep -rn "GlobalPath\|rewriteGlobalFile\|ensureGlobalFile\|syncGlobal\|syncAllGlobals\|groupFilePath\|filepathDir" --include='*.go' .
```

Expected: no output.

- [ ] **Step 5: Run the whole suite**

Run: `go vet ./... && go test -race ./...`

Expected: PASS, including both tests from Tasks 1 and 2.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/global.go internal/cli/shared.go internal/cli/sync.go internal/config/paths.go
git commit -m "refactor: delete the global.env file layer

rewriteGlobalFile existed to keep file and database in step and was
never called. Rather than add the missing calls in three more places,
remove the file: the database is now the only source of truth for the
group layer, so there is nothing left to go stale."
```

---

### Task 4: Note the migration in the README

The README turns out to need almost nothing. `README.md:181` describes the four
layers in precedence order and says nothing about files or `shared-sync`, so it
stays exactly as it is. `docs/prizm-roadmap.md` has no matching passage. The
only real gap is that `shared/<group>/` is documented as "the bag files you edit
by hand" while a `global.env` may still be sitting beside them.

**Files:**
- Modify: `README.md:304-312` (the data-location table and the paragraph under it)

**Interfaces:**
- Consumes: nothing. Documentation only.
- Produces: nothing.

- [ ] **Step 1: Confirm there is nothing else to change**

Run:

```bash
grep -n "global.env\|prizm global\|shared-sync\|group-global" README.md docs/prizm-roadmap.md
```

Expected: exactly one hit, `README.md:181`, the layer-precedence sentence. It is
accurate — leave it alone. If this command returns anything else, a passage was
added since this plan was written: apply the same rule to it, which is that the
group layer lives in the database and is edited with `prizm global` or
`prizm edit --global`, while **bag** files remain hand-edited and are correct as
documented.

- [ ] **Step 2: Add the migration note**

In `README.md`, immediately after the paragraph ending "…rather than replaced."
(line 312), insert:

```markdown
Older versions also kept the group-global layer in `shared/<group>/global.env`,
loaded by `shared-sync`. That layer now lives in `prizm.db` and applies the
moment you save, so a `global.env` left in your data directory is inert and can
be deleted.
```

- [ ] **Step 3: Verify nothing else broke**

Run: `go vet ./... && go test -race ./...`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: note that global.env is inert"
```

---

## Verification

After all four tasks:

- [ ] `go vet ./... && go test -race ./...` passes
- [ ] `grep -rn "global.env" --include='*.go' .` returns exactly three hits, all
      in `internal/cli/global_test.go` (currently around lines 36, 47, and 63 —
      line numbers may drift) — two doc comments plus a deliberate fixture
      path, not production code, and not a regression to fix
- [ ] Manual check against a scratch group, which is the original repro:

```bash
export XDG_DATA_HOME=$(mktemp -d)
prizm init demo
prizm add-repo demo api --path "$(mktemp -d)"
prizm add-workflow demo local
prizm shared-add demo local bag --repos api --file "$(mktemp -d)/bag.env"
prizm var demo --global REGION=us-east-2
prizm shared-sync demo local bag --yes
prizm edit demo --global      # EDITOR=cat, or just confirm REGION is listed
# REGION must still be us-east-2
```

## What this plan does not do

The cross-layer editor itself. This is only its prerequisite. The editor gets its own plan once this lands, covering `internal/editmodel` (the headless change algebra, promote solver and reach invariant) and `internal/cli/editall.go` (the bubbletea matrix with pick-and-drop and staged edits).
