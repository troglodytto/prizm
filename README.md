# prizm

Share environment files across multiple repos, grouped by the workflow you want to run.

```bash
prizm XYZ local
```

…sets the `local` environment for every repo that workflow covers — frontend, backend, auth, ai — each from its own layered configuration, in one command.

> **Status: pre-release scaffold.** The design is complete and written up; the command tree is not wired up yet. See [`docs/prizm-roadmap.md`](docs/prizm-roadmap.md).

## The idea

A **group** owns **repos** (each at a fixed path) and **workflows**. A workflow is a named bundle: an explicit subset of repos plus their variables. `prizm <group> <workflow>` resolves each repo's variables, renders an env file, and symlinks it into place.

Variables merge in three layers, most specific winning:

1. **repo-shared** — applies in every workflow touching that repo
2. **shared bag** — a named set of variables scoped to `(workflow, repo subset)`
3. **repo + workflow** — the specific case

Shared bags solve cross-repo duplication. Values may reference each other, so a database URL can be *derived* once and consumed under whatever name each repo expects:

```sh
# shared bag 'db' — backend, auth, ai
_PRIZM_DB_USER = svc_app
_PRIZM_DB_PASS = hunter2
_PRIZM_DB_URL  = postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app
```

```sh
# backend/.env — the only line that lands on disk
DB_URL=postgres://svc_app:hunter2@localhost:5432/app
```

Keys prefixed `_PRIZM_` are internal: referenceable from any template, never written to disk. The emitted file carries no trace of where a value came from.

## Design

Five phases, each shipping working software:

| Phase | Delivers |
| --- | --- |
| 1 | Storage, groups/repos/workflows, variable layers, interpolation, `up`, dynamic shell completion |
| 2 | History, drift detection, `status`, `sync`, `--dry-run`, `repair` |
| 3 | Interactive picker, multi-select, forms, conflict resolution |
| 4 | Version carousel, per-key diffs, restore |
| 5 | Compose services per workflow, `down` |

Plans live in [`docs/superpowers/plans/`](docs/superpowers/plans/), and the design conversation they came from is in [`prizm-design-brainstorm-transcript.md`](prizm-design-brainstorm-transcript.md).

## Install

```bash
go install github.com/troglodytto/prizm@latest
```

## License

MIT
