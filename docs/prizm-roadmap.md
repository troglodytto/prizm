# prizm — Phase Roadmap

Five phases. Each one ends with working, shippable software; none is a refactor-only step. The spec for all of them is `prizm-design-brainstorm-transcript.md`, plus the decisions recorded in each plan.

**All five have shipped, as of v0.5.0.** The table is kept as the record of what each phase was for; the plans record the reasoning, including the places execution disagreed with them.

| Phase | Plan | Delivers | Charm? |
| --- | --- | --- | --- |
| 1 ✅ | [`plans/2026-08-27-prizm-core.md`](superpowers/plans/2026-08-27-prizm-core.md) | Storage, groups/repos/workflows, three variable layers, interpolation, `up`, dynamic completion, shared output style | Lip Gloss only |
| 2 ✅ | [`plans/2026-08-27-prizm-reconciliation.md`](superpowers/plans/2026-08-27-prizm-reconciliation.md) | History engine, drift detection, `status`, `sync`, `--dry-run`, `repair`, apply lock | No |
| 3 ✅ | [`plans/2026-08-27-prizm-tui.md`](superpowers/plans/2026-08-27-prizm-tui.md) | The Charm layer: picker, multi-select, form, interactive `sync`, live `up` | Yes |
| 4 ✅ | [`plans/2026-08-27-prizm-audit.md`](superpowers/plans/2026-08-27-prizm-audit.md) | `audit` version carousel, key-level diffs, `--restore` | Yes |
| 5 ✅ | [`plans/2026-08-27-prizm-docker.md`](superpowers/plans/2026-08-27-prizm-docker.md) | Compose services per workflow, `up` bring-up, `down`, container status | Spinner only |

## Why this order

**Engine before interface.** Phase 2 builds the comparison and attribution machinery — what is on disk, what prizm would write, which layer owns each key. Phase 3's interactive `sync` is a *renderer* for that machinery. Building the TUI first would mean designing screens for logic that does not exist yet, then rewriting them.

**History starts early, on purpose.** Snapshot-on-write lands in Phase 2 (Task 1), not in Phase 4 with the rest of `audit`. History is only useful if it was already being recorded when you needed it; if snapshots arrived in Phase 4, every edit made during Phases 1–3 would be unrecoverable. The engine is one task; the carousel that reads it is the part that waits for Charm.

**Docker last.** The spec treats it as a separate lifecycle with separate failure modes, and it is the only phase whose absence costs nothing — env application is complete and correct without it.

## The UI philosophy, in one paragraph

The CLI is primary and the completion path is sacred. Most of prizm's interaction budget is spent on Tab, which must stay instant, so nothing on the completion path ever decrypts, prompts, or renders. Charm appears at exactly the moments where a person needs to *see a set and choose from it*: picking a workflow out of many, ticking which repos a bundle covers, choosing which side of a conflict wins, stepping through history. Every one of those surfaces has a non-interactive equivalent behind flags, because a tool that can only be driven by a human is a tool that cannot be scripted, and `up` in particular must stay usable from a shell alias with no TTY at all. The full design is in the Phase 3 plan.

## Cross-phase invariants

These hold from Phase 1 onward and no later phase may break them:

1. **Values are encrypted at rest; metadata is not.** Completion queries touch plaintext columns only.
2. **The store holds templates, never expansions.** `Merge` → `Expand` → `Emit` stay three separate steps, because `sync` and `audit` compare templates while `up` compares outputs.
3. **Internal-ness lives in the key name** (`_PRIZM_` prefix), so it cannot be lost by an override.
4. **Repo paths are a stable contract.** Only `repair` changes one.
5. **Divergence is reported, never auto-resolved.** `up` warns and finishes; reconciliation happens only when explicitly asked for.
6. **Every interactive surface has a flag-driven equivalent.**
7. **One visual language.** Every line prizm prints — plain text from Phase 1, rendered screens from Phase 3 — goes through `internal/style`: one glyph set, one palette, one column width. Colour degrades to plain text off a terminal and under `NO_COLOR`.
8. **A failing repo never leaves a half-written env file.** Failures are per-repo and leave the previous state intact.
