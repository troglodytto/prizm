# prizm

[![ci](https://github.com/troglodytto/prizm/actions/workflows/ci.yml/badge.svg)](https://github.com/troglodytto/prizm/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/troglodytto/prizm?sort=semver)](https://github.com/troglodytto/prizm/releases)
[![go reference](https://pkg.go.dev/badge/github.com/troglodytto/prizm.svg)](https://pkg.go.dev/github.com/troglodytto/prizm)

**Make envs bearable again.**

Share environment files across repos, grouped by the workflow you want to run.

```bash
prizm <group> <workflow>
```

Both are names you pick. A **group** is one project spread across several repos; a **workflow** is a named bundle of those repos plus the variables they need.

Throughout this README the group is called `my-saas-platform` and it owns four repos — `frontend`, `backend`, `auth` and `ai`. Substitute your own:

```bash
prizm my-saas-platform local
```

One command sets all four up, each with its own env file, built from its own layered configuration and symlinked into place.

![prizm ls my-saas-platform](docs/screenshots/ls.png)

> **v0.6.2.** In daily use. Groups, repos and workflows; four layers of variables with interpolation; `status`, `sync`, `audit` with restore, `$EDITOR` editing, dry runs, an interactive picker, compose services, and directory-aware completion for all of it.

## Contents

- [The problem](#the-problem) — why this exists
- [How this differs](#how-this-differs) — direnv, Doppler, and when to use those instead
- [Installation](#installation) — one curl command, or `go install`
- [Getting started](#getting-started) — working setup in six lines
- [How it works](#how-it-works) — groups, workflows, and the four layers
- [Sharing values across repos](#sharing-values-across-repos) — one edit, every consumer
- [Usage](#usage) — status, dry runs, sync, history, the picker
- [Docker services](#docker-services) — bring a stack up with a workflow
- [Shell completion](#shell-completion)
- [Design notes](#design-notes) · [Where your data lives](#where-your-data-lives) · [Verifying a download](docs/verifying.md)

## The problem

You have a project spread across several repos. Running it locally means a `.env` in each one, and those files drift: the database URL is duplicated in three of them, one is stale, and the only way to find out is when something fails at 2am.

Copying files around doesn't fix it, because the *sets* differ. Working on the frontend against a deployed backend needs one repo configured. Running the full stack needs four. Debugging payments needs two. These aren't environments — they're workflows, and each one is a different bundle of repos.

## How this differs

The space is crowded, and most of it solves a neighbouring problem well:

| | Good at | Why not this |
| --- | --- | --- |
| **direnv**, **mise** | loading env when you `cd` into a directory | one directory at a time — nothing composes values *across* repos |
| **Doppler**, **Infisical**, **EnvKey** | one value, many consumers, with a team and an audit trail | organised by *environment*; needs an account and a running service |
| **dotenvx**, **SOPS** | keeping secrets encrypted inside a repo | a single repo's file, not a set of them |
| **Turborepo**, **Nx** | sharing config inside a monorepo | assumes one repo |

prizm's axis is the **workflow**: a named, arbitrary subset of separately
cloned repos, configured together by one command. "Frontend against deployed
QA" and "full stack locally" are not environments — they are different slices
of the same repos, and that is the thing the models above do not have a shape
for. It is local-first as well: no account, no service, nothing leaves your
machine.

**Use something else if** you are in a monorepo — a root `.env` and your build
tool already do this — or if you need shared secrets for a team with access
control and an audit trail, which Doppler and Infisical are built for and this
deliberately is not.

## Installation

No Go toolchain needed — this fetches a prebuilt binary into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/troglodytto/prizm/main/install.sh | sh
```

If piping a script from the internet into a shell makes you uneasy — and it
reasonably might — download it and read it:

```bash
curl -fsSLO https://raw.githubusercontent.com/troglodytto/prizm/main/install.sh
less install.sh
```

Then, once you have decided you are happy with it:

```bash
sh install.sh
```

It never calls sudo, and it checks every download against the release's
published SHA-256 before unpacking. `PRIZM_INSTALL_DIR` and `PRIZM_VERSION`
override where and what it installs.

Prefer to build it yourself, or want a specific commit:

```bash
go install github.com/troglodytto/prizm@latest
```

That path needs **Go 1.25 or newer** and a C compiler, since storage is SQLite
through cgo. The prebuilt Linux binaries are statically linked and need
neither.

| | |
| --- | --- |
| **Linux** | amd64, arm64 — static, runs on any distro |
| **macOS** | Apple silicon and Intel |
| **Windows** | not yet — the apply lock uses `flock`. [Open an issue](https://github.com/troglodytto/prizm/issues) if you want it |

Builds are attached to each [GitHub release](https://github.com/troglodytto/prizm/releases)
with checksums, which the installer verifies. GPG signing is wired up but not
yet enabled — [docs/verifying.md](docs/verifying.md) covers checking a download
by hand.

Values are encrypted with a key from your OS keychain. The keychain is only
consulted when a command actually reads or writes a value — `--version`,
`--help`, `init` and `ls` work on a headless box with no keychain at all.

## Getting started

Point prizm at two repos, describe one workflow, apply it:

```bash
prizm init my-saas-platform
prizm add-repo my-saas-platform ~/code/frontend
prizm add-repo my-saas-platform ~/code/backend

prizm add-workflow my-saas-platform local   # ticks every repo; ⏎ accepts

prizm var my-saas-platform backend  DATABASE_URL=postgres://localhost/dev PORT=4000
prizm var my-saas-platform frontend NEXT_PUBLIC_API_URL=http://localhost:4000

prizm my-saas-platform local
```

Both repos now have a `.env`. `prizm status my-saas-platform` shows where they
stand, and `prizm my-saas-platform local --dry-run` shows what a run would
change before it changes anything.

Every prompt has a flag that skips it, so the same lines work in a script with
no terminal attached — `add-workflow` without `--repos` covers every repo.

Already have `.env` files worth keeping? Import instead of retyping:

```bash
prizm import my-saas-platform backend ~/code/backend/.env.local
```

## How it works

A **group** owns **repos** — each pinned to a fixed path — and **workflows**. A workflow is a named bundle: an explicit subset of repos plus their variables.

```bash
prizm init <group>
prizm add-repo <group> <repo> --path <path>
prizm add-workflow <group> <workflow> [--repos <a,b>] [--tag prod|qa|local]
```

Which, with the names used here, is:

```bash
prizm init my-saas-platform
prizm add-repo my-saas-platform frontend --path ~/code/frontend
prizm add-repo my-saas-platform backend  --path ~/code/backend

prizm add-workflow my-saas-platform local                          # every repo
prizm add-workflow my-saas-platform frontend-only --repos frontend # just one
prizm add-workflow my-saas-platform production --tag prod          # guardrailed
```

Then switch between them:

```bash
prizm my-saas-platform local            # all four repos configured
prizm my-saas-platform frontend-only    # only the frontend; nothing else touched
```

Variables merge in four layers, most specific winning: **group-global** (true everywhere in the group) → **repo-shared** (every workflow touching that repo) → **shared bag** (a named set scoped to a workflow and a subset of repos) → **repo + workflow** (the specific case).

The split is what makes switching cheap. The wiring — `MONGO_URI=${_PRIZM_MONGO_URI}` — is written once per repo. Only the bag changes per environment, so the same line resolves to a different database in each.

## Sharing values across repos

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

## Usage

**Where does everything stand?**

```bash
prizm status my-saas-platform
```

![prizm status my-saas-platform](docs/screenshots/status.png)

Which workflow each repo is on, and which files have been hand-edited since.

**What would this change?**

```bash
prizm my-saas-platform local --dry-run
```

![prizm my-saas-platform local --dry-run](docs/screenshots/dry-run.png)

Nothing is written. A repo covered by the workflow but holding no variables for it is flagged rather than given a green tick — a silent gap in a tool that writes prod config is what bites someone at 2am.

**You hand-edited a `.env`. Keep it.**

```bash
prizm sync my-saas-platform auth
```

![prizm sync my-saas-platform auth](docs/screenshots/sync.png)

`sync` works out which layer each edit belongs to. A value that came from a shared bag is the interesting case, and it asks rather than guessing:

![the sync decision list](docs/screenshots/resolve.png)

`←→` cycles the answer for a row, `↑↓` moves between them, `⏎` applies them all. Changing the shared value moves every repo using that bag; pinning breaks the link for this one repo only.

**What did this used to be?**

```bash
prizm audit my-saas-platform auth
```

![prizm audit my-saas-platform auth](docs/screenshots/audit.png)

Every write records the state it produced, so history exists before you think to ask for it. `--restore` turns that list into a carousel:

![the history carousel](docs/screenshots/carousel.png)

`←→` scrubs; each version shows what restoring it would do to the *current* state, in the direction it would move. The restore is itself a version, so an unwanted one undoes the same way.

**Just show me the options.**

```bash
prizm my-saas-platform
```

![the workflow picker](docs/screenshots/picker.png)

`/` filters, `⏎` applies, and `e` opens that repo's layer in `$EDITOR` instead — the same path `prizm edit` takes.

## Docker services

A workflow can carry a compose stack, brought up after the env files are written:

```bash
prizm docker my-saas-platform local --compose ./local.yml --services db-tunnel
prizm my-saas-platform local     # writes the env files, then starts db-tunnel
prizm down my-saas-platform local
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
- **Applies are exclusive.** A single lock covers the whole installation, so two `up` runs cannot interleave; the second fails immediately rather than waiting.

## Where your data lives

Everything is under `~/.local/share/prizm` (or `$XDG_DATA_HOME/prizm`), created
owner-only:

| Path | What |
| --- | --- |
| `prizm.db` | groups, repos, workflows, variables — values encrypted |
| `shared/<group>/` | the bag files you edit by hand |
| `built/<group>/<workflow>/` | the generated env files your repos link to |

A managed repo's `.env` is a **symlink** into `built/`. The first time prizm
takes over a repo that already had a real `.env`, it is renamed to
`.env.prizm-backup.<timestamp>` rather than replaced.

Older versions also kept the group-global layer in `shared/<group>/global.env`,
loaded by `shared-sync`. That layer now lives in `prizm.db` and applies the
moment you save, so a `global.env` left in your data directory is inert and can
be deleted.

Values are AES-256-GCM encrypted with a key held in your OS keychain; variable
*names* stay in plaintext, which is what keeps shell completion instant. Nothing
is sent anywhere — prizm has no network access and no account.

On Linux the keychain is a Secret Service provider (gnome-keyring, KWallet), so
a headless session may not have one. Only commands that touch a value need it,
and the error says so if you hit it.

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
