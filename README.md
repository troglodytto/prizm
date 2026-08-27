# prizm

Share environment files across repos, grouped by the workflow you want to run.

```bash
prizm platform local
```

One command sets up `frontend`, `backend`, `auth` and `ai` — each with its own env file, built from its own layered configuration, symlinked into place.

![prizm ls platform](docs/screenshots/ls.png)

> **v0.5.0.** In daily use. Groups, repos and workflows; four layers of variables with interpolation; `status`, `sync`, `audit` with restore, `$EDITOR` editing, dry runs, an interactive picker, compose services, and directory-aware completion for all of it.

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

Variables merge in four layers, most specific winning: **group-global** (true everywhere in the group) → **repo-shared** (every workflow touching that repo) → **shared bag** (a named set scoped to a workflow and a subset of repos) → **repo + workflow** (the specific case).

The split is what makes switching cheap. The wiring — `MONGO_URI=${_PRIZM_MONGO_URI}` — is written once per repo. Only the bag changes per environment, so the same line resolves to a different database in each.

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

## Day to day

**Where does everything stand?**

```bash
prizm status platform
```

![prizm status platform](docs/screenshots/status.png)

Which workflow each repo is on, and which files have been hand-edited since.

**What would this change?**

```bash
prizm platform local --dry-run
```

![prizm platform local --dry-run](docs/screenshots/dry-run.png)

Nothing is written. A repo covered by the workflow but holding no variables for it is flagged rather than given a green tick — a silent gap in a tool that writes prod config is what bites someone at 2am.

**You hand-edited a `.env`. Keep it.**

```bash
prizm sync platform auth
```

![prizm sync platform auth](docs/screenshots/sync.png)

`sync` works out which layer each edit belongs to. A value that came from a shared bag is the interesting case, and it asks rather than guessing:

![the sync decision list](docs/screenshots/resolve.png)

`←→` cycles the answer for a row, `↑↓` moves between them, `⏎` applies them all. Changing the shared value moves every repo using that bag; pinning breaks the link for this one repo only.

**What did this used to be?**

```bash
prizm audit platform auth
```

![prizm audit platform auth](docs/screenshots/audit.png)

Every write records the state it produced, so history exists before you think to ask for it. `--restore` turns that list into a carousel:

![the history carousel](docs/screenshots/carousel.png)

`←→` scrubs; each version shows what restoring it would do to the *current* state, in the direction it would move. The restore is itself a version, so an unwanted one undoes the same way.

**Just show me the options.**

```bash
prizm platform
```

![the workflow picker](docs/screenshots/picker.png)

`/` filters, `⏎` applies, and `e` opens that repo's layer in `$EDITOR` instead — the same path `prizm edit` takes.

## Services

A workflow can carry a compose stack, brought up after the env files are written:

```bash
prizm docker platform local --compose ./local.yml --services db-tunnel
prizm platform local     # writes the env files, then starts db-tunnel
prizm down platform local
```

Docker is deliberately best-effort and reported separately: if the daemon is closed, the env files are still written and `up` still succeeds. Each workflow gets its own compose project, so two workflows can share a compose file without adopting each other's containers.

## Design notes

A few decisions worth knowing before you rely on it:

- **Secrets are encrypted at rest.** Values are AES-256-GCM encrypted with a key held in your OS keychain. Names stay in plaintext so shell completion is instant.
- **Repo paths are a contract.** They're stored absolute and never change on their own; `prizm repair` is the one escape hatch when a checkout moves.
- **Nothing is overwritten silently.** An existing `.env` is backed up before prizm takes over, and hand-edits are reported rather than clobbered.
- **Drift is reported, never auto-resolved.** Applying a workflow stays fast and predictable; reconciliation only happens when you ask for it.
- **Everything scriptable.** Every interactive prompt has a flag equivalent, so prizm works the same in a shell alias or CI with no terminal attached.
- **Ambiguity is refused, not guessed.** A command that could mean two repos, two layers, or two workflows stops and says which — quietly acting on the first one is how a destructive command becomes a surprise.
- **Applies are exclusive.** Two `up` runs cannot interleave writes across the same repos; the second fails immediately rather than waiting.

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

## Screenshots

Every image above is the real output of the command beside it, rendered by
[`docs/screenshots/generate.sh`](docs/screenshots/generate.sh) against a
throwaway sandbox. Regenerate them after a change to the visual layer:

```bash
go install github.com/charmbracelet/freeze@latest
./docs/screenshots/generate.sh
```

Nothing is hand-edited, which is the point — a doctored screenshot stops
being evidence that the tool looks like that.

## License

MIT © Piyush Upadhyay
