# prizm

Share environment files across repos, grouped by the workflow you want to run.

```bash
prizm platform local
```

One command sets up `frontend`, `backend`, `auth` and `ai` — each with its own env file, built from its own layered configuration, symlinked into place.

> **Pre-release.** The design is settled and the foundations are landing; the command tree isn't wired up yet. `v0.1.0` will be the first usable release.

## The problem

You have a project spread across several repos. Running it locally means a `.env` in each one, and those files drift: the database URL is duplicated in three of them, one is stale, and the only way to find out is when something fails at 2am.

Copying files around doesn't fix it, because the *sets* differ. Working on the frontend against a deployed backend needs one repo configured. Running the full stack needs four. Debugging payments needs two. These aren't environments — they're workflows, and each one is a different bundle of repos.

## The model

A **group** owns **repos** — each pinned to a fixed path — and **workflows**. A workflow is a named bundle: an explicit subset of repos plus their variables.

```bash
prizm init platform
prizm add-repo platform frontend --path ~/code/frontend
prizm add-repo platform backend  --path ~/code/backend

prizm add-workflow platform local                          # every repo
prizm add-workflow platform frontend-only --repos frontend # just one
prizm add-workflow platform production --tag prod          # guardrailed
```

Then switch between them:

```bash
prizm platform local            # all four repos configured
prizm platform frontend-only    # only the frontend; nothing else touched
```

Variables merge in three layers, most specific winning: **repo-shared** (every workflow touching that repo) → **shared bag** (a named set scoped to a workflow and a subset of repos) → **repo + workflow** (the specific case).

## Derived shared values

Shared bags are what stop the same database URL living in four places. They hold the *recipe*, not the result — so values can be built from other values, and editing one input updates every consumer at once:

```sh
# a shared bag, covering backend, auth and ai
_PRIZM_DB_USER = svc_app
_PRIZM_DB_PASS = hunter2
_PRIZM_DB_URL  = postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app
```

Each repo then exposes it under whatever name its own code expects:

```sh
# backend, in the local workflow
DB_URL = ${_PRIZM_DB_URL}

# auth, in the local workflow
DATABASE_URL = ${_PRIZM_DB_URL}
```

What lands on disk is one line, with no trace of where it came from:

```sh
# backend/.env
DB_URL=postgres://svc_app:hunter2@localhost:5432/app
```

Keys prefixed `_PRIZM_` are internal — referenceable from any template, never written to a file. Rotating the password is one edit in one place.

## Design notes

A few decisions worth knowing before you rely on it:

- **Secrets are encrypted at rest.** Values are AES-256-GCM encrypted with a key held in your OS keychain. Names stay in plaintext so shell completion is instant.
- **Repo paths are a contract.** They're stored absolute and never change on their own; `prizm repair` is the one escape hatch when a checkout moves.
- **Nothing is overwritten silently.** An existing `.env` is backed up before prizm takes over, and hand-edits are reported rather than clobbered.
- **Drift is reported, never auto-resolved.** Applying a workflow stays fast and predictable; reconciliation only happens when you ask for it.
- **Everything scriptable.** Every interactive prompt has a flag equivalent, so prizm works the same in a shell alias or CI with no terminal attached.

## Install

```bash
go install github.com/troglodytto/prizm@latest
```

Requires Go 1.23 or newer.

## Shell completion

```bash
prizm completion zsh  > "${fpath[1]}/_prizm"     # zsh
prizm completion bash > /etc/bash_completion.d/prizm
prizm completion fish > ~/.config/fish/completions/prizm.fish
```

Completion is directory-aware: the group whose repo you're standing in sorts first, then the ones you use most.

If you'd rather type something shorter, alias it — and point completion at the alias too, or you lose it:

```bash
alias pzm=prizm
compdef pzm=prizm         # zsh
complete -F _prizm pzm    # bash
```

## License

MIT © Piyush Upadhyay
