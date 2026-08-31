# Cross-layer editor — design

**Status:** approved design, not yet planned
**Date:** 2026-08-31

One screen for every variable in a group, across every workflow, every repo and
the group-global layer — and the ability to move a value to where it belongs
without leaving it.

## The problem

A group's variables are spread across four layers and, in practice, mostly
duplicated. Measured on the `kroolo` group: **950 definitions across 220
distinct key names, of which 189 key names hold one identical value at every
site.** 756 of those 950 definitions are redundant copies of a value defined
elsewhere.

Nothing in prizm today shows that. `prizm edit` opens one layer for one repo in
one workflow; seeing whether `REDIS_HOST` agrees across its 15 sites means
opening 15 layers and comparing by eye. So duplication accumulates silently,
and the layered model — the reason the tool exists — goes unused. `group_vars`
was empty on a group with 30 group-wide constants in it.

The fix is a view that makes the spread legible, plus one gesture that collapses
it.

## What it is not

Not a general env editor. It edits prizm's layers, not repos' `.env` files —
those stay generated. Not a multi-group view: one group fills the screen.

## Interaction model

**A matrix, one workflow at a time.** Rows are keys, columns are the layers
visible to that workflow: `global`, then its bags, then its repos. One workflow
at a time is what makes the columns fit a terminal; the alternative — all 23
repo×workflow sites across one axis — does not.

**Pick and drop.** Pick a cell, move with arrows, drop. Switching workflow keeps
what you are holding, so a cross-workflow move is pick → switch → drop. Drop is
directional and spatial rather than a menu choice.

Where you drop decides which operation runs:

| Drop target | Operation |
| --- | --- |
| another repo cell, same layer | **move** — the definition relocates; reach changes, and the diff says so |
| a broader layer (`global`, a bag, the repo-shared band) | **promote** — see below; the solver proposes a layer, `←→` overrides it |

`[p]` on a focused cell is the same promotion without the pick-and-move, for
when you want the solver to choose the target outright.

**Values are masked.** Cells render the `••••fingerprint` that
`internal/style/secret.go` already produces everywhere else. That is enough to
see at a glance which sites agree, which is the question the matrix exists to
answer. `[v]` reveals only the focused cell; `[enter]` opens an inline edit.
Because promotion moves values by reference, most work never puts a value on
screen at all.

**Edits are staged.** Changes accumulate in a pending rail and touch nothing
until applied. Applying shows the full diff and which env files it would
rewrite, and records **one** snapshot for the session — so an unwanted editing
session undoes as a single unit rather than as 566 entries in `audit`.

## Promotion, and why it leaves a reference

Promoting a value moves it to a broader layer under an internal name and leaves
a reference behind:

```
before   workos-qa/backend   MONGO_URI=mongodb://…
after    group-global        _PRIZM_MONGO_URI=mongodb://…
         workos-qa/backend   MONGO_URI=${_PRIZM_MONGO_URI}
```

This is the wiring the README already documents, applied automatically.

**Every site holding an identical value is converted, not just the picked one.**
That is where the collapse comes from: `REDIS_HOST` is identical at 15 sites, so
one promotion produces a single `_PRIZM_REDIS_HOST` and 15 references, taking
the group from 15 definitions of that value to one. Applied across the 189 keys
that agree everywhere, this is the 756 → 189 collapse. The count is shown before
applying, and the absorbed set is listed so it can be narrowed.

**It is reach-preserving by construction.** The key stays defined at exactly the
sites it was defined at; only its value became a reference. Compare the
alternative — moving the definition up and deleting the redundant siblings —
which silently changes *which repos a key reaches*. That alternative was
attempted during the session that produced this design: promoting 30 keys to
group-global would have written `PROPEL_AUTH_CLIENT_SECRET`,
`CRYPTO_ENCRYPTION_KEY`, `STRIPE_API_KEY` and `PINECONE_API_KEY` into repos that
never had them. Spreading credentials to more files on disk is not a cleanup.
The reference model makes that outcome unreachable rather than something a
warning has to catch.

It is also invisible on disk: `resolve.Emit` strips `_PRIZM_*`, so the promoted
value is never written to any repo's env file.

### The solver

The two middle layers widen along **different axes** and do not stack:

```
              group-global          all repos × all workflows
              /          \
     repo-shared          bag
   one repo,             one workflow,
   all workflows         subset of repos
              \          /
            workflow-repo            one repo × one workflow
```

So "one layer up" is ambiguous, and asking the user to resolve it each time
pushes a coverage calculation onto them. Instead prizm solves it.

Because the promoted variable is internal and emits nowhere, it does not need a
layer that covers the sites *exactly* — only one **visible** at all of them.
Broader is harmless.

```
visible(bag B)         = (workflow(B), repos(B))
visible(repo-shared r) = (r, every workflow touching r)
visible(global)        = everything

Solve(S) = smallest visibility set ⊇ S
  ∃ bag B with S ⊆ visible(B)  → that bag, smallest such
  all sites share one repo r   → repo-shared :: r
  otherwise                    → group-global
```

prizm shows the layer it picked and why, and `←→` overrides it.

### Edge cases

- **Already a reference.** If the picked value is `${_PRIZM_X}`, promote
  `_PRIZM_X` itself one layer up. Never produce `${_PRIZM__PRIZM_X}`.
- **Name collision.** The default name is `_PRIZM_<KEY>`. If that name already
  exists holding a *different* value, prompt. Silently merging two distinct
  values under one name would be data loss.
- **Partial agreement.** If only some sites agree, promote offers the agreeing
  subset and leaves the rest. `MONGO_URI` has 4 distinct values across 15 sites;
  the largest group is 5.

## Components

**`internal/editmodel`** — headless, no terminal, fully unit-testable.

```go
type Site struct { Layer LayerKind; Repo, Workflow string; BagID int64 }
type Def  struct { Site Site; Key string; FP string }

func Load(s *store.Store, group string) (*Model, error)
func (m *Model) Reach(key string) map[RepoWorkflow]bool
func (m *Model) Solve(key string, sites []Site) (Layer, string)
func (m *Model) Plan(changes []Change) (Plan, error)
```

`Change` covers `Move · Promote · Rename · Add · Delete · SetValue`. `Plan`
reports the writes, the reach delta and the definition count before and after;
the apply screen is `Plan` rendered.

**`internal/cli/editall.go`** — the bubbletea view. Matrix, held cell, pending
rail. Reuses `style.Secret`, `tui.Theme`, `resolve.IsInternal`.

The split follows the reasoning already stated in `internal/resolve`'s package
comment — the stages "stay separate on purpose … both need a place to stop."
The risky part here is the change algebra, not the drawing, and `tui.Available()`
is false under test. Keeping the algebra headless is what makes it testable.

## The invariant

> For every key, the set of `(repo, workflow)` pairs it resolves into is
> identical before and after.

`Promote`, `Move` and `Rename` must preserve reach. `Add` and `Delete` change it
by definition and say so. This is the property worth testing directly: generate
random change sets, assert reach equality.

## Prerequisite: retire `global.env`

**This ships first, on its own.**

`internal/config.GlobalPath` defines a file backing the group layer, and
`internal/cli/global.go:172` defines `rewriteGlobalFile` to keep that file in
step with the database. **`rewriteGlobalFile` is never called** — it is dead
code. So `global.env` goes stale the moment any group-global variable is
written, and `shared-sync` — which treats the file as source of truth, and which
reconciles the group layer even when scoped to a single bag — then deletes every
group-global variable from the database.

Reproduced:

```
prizm var kroolo --global _PRIZM_CANARY=…
  → group_vars=1, global.env unchanged at 110 bytes, canary absent from file
prizm shared-sync kroolo workos-qa ports --yes     # scoped to ONE bag
  → "✓ kroolo (global) synced"
  → group_vars=0        ← every group-global variable destroyed
```

Under this design nearly every promotion writes group-global, which turns an
edge case into a loaded gun. The fix is to delete the file-backed group layer
rather than patch one instance of the desync:

- delete `config.GlobalPath` and `rewriteGlobalFile`
- `shared-sync` reconciles bags only, never the group layer
- the database becomes the sole source of truth for group-global; `var --global`
  and `edit --global` remain the edit paths

Regression test: `var --global` followed by `shared-sync`, assert `group_vars`
survives.

## Testing

`editmodel` needs no TTY. Golden tests for the solver across the bag,
repo-shared and global cases plus both edge cases; a property test for the reach
invariant; the `global.env` regression test above. The view gets render tests in
the style of the existing `internal/tui/render_test.go`.

## Deliberately cut

No cross-group view. No in-editor undo history beyond the session snapshot —
`audit --restore` already does this. No value history in cells.
