# wt-helper

**Because `git worktree add` should be smarter than you are at 3am.**

A CLI tool that makes git worktrees actually usable for parallel agent workflows. It copies or renders your `.envrc` when you spawn a worktree and cleans up your mess when you delete one.

## What it does

```
git wt-add       → "PORT [8080] = ?  SECRET [default] = ?"
git worktree add → "here's your .envrc, I already direnv-allow'd it, you're welcome"
git wt-remove    → "let me run your cleanup script before I nuke this"
```

No more forgetting to copy `.envrc`. No more orphaned docker containers named `dev-feature-branch-42` haunting your machine like digital ghosts. For template repos, `git wt-add` prompts for vars BEFORE creating the worktree — interactive, with defaults, no surprises.

## Prerequisites

- [direnv](https://direnv.net/) — for loading `.envrc` (optional but recommended, like seatbelts)
- [jq](https://stedolan.github.io/jq/) — only if you're doing the juju thing below
- A git repository — presumably you have one of these

## Installation

```bash
go install github.com/guillaume-fouillet/wt-helper@latest
```

Or if you like to live dangerously:

```bash
git clone https://github.com/guillaume-fouillet/wt-helper.git
cd wt-helper
go build -o wt-helper .
mv wt-helper ~/.local/bin/
```

## Usage

### One-time setup

Point it at a repo and tell it what to copy and what to run on cleanup:

```bash
# The full monty (template + wrapup)
wt-helper setup --envrc-file .envrc.template --vars-file .wt-helper.vars.json --wrapup-cmd ./scripts/cleanup.sh

# Plain envrc (no template, just copy)
wt-helper setup --envrc-file .envrc

# Just wrapup (I'm a chaos agent who doesn't use direnv)
wt-helper setup --wrapup-cmd ./scripts/cleanup.sh

# Point at a different repo (because you have many repos, you overachiever)
wt-helper setup --target /path/to/repo --envrc-file .envrc --wrapup-cmd ./cleanup.sh

# "I know what I'm doing, overwrite my hooks"
wt-helper setup --envrc-file .envrc --wrapup-cmd ./cleanup.sh --force
```

### Template mode

If your `.envrc` contains `{{`, wt-helper treats it as a Go template. Each worktree gets its own rendered values — unique ports, unique DB names, unique everything.

```bash
# With a vars file (JSON, TOML, or YAML)
wt-helper setup --envrc-file .envrc.template --vars-file .wt-helper.vars.json

# Auto-detected vars file in repo root (.wt-helper.vars.{json,toml,yaml})
wt-helper setup --envrc-file .envrc.template

# Agent swarm mode: fail if any var is missing (no interactive prompts)
wt-helper setup --envrc-file .envrc.template --vars-file agent-1.vars.json --auto

# Prompt for missing vars interactively
wt-helper setup --envrc-file .envrc.template
# → PORT = 8080
# → DB_DRIVER = sqlite
```

**Built-in variables** (always available):

| Variable | Example |
|----------|---------|
| `{{.WorktreeName}}` | `feature-auth` |
| `{{.WorktreePath}}` | `/home/you/repos/../feature-auth` |
| `{{.BranchName}}` | `feature-auth` |

**Vars file formats** (auto-detected by extension):

```json
// .wt-helper.vars.json
{"PORT": "8080", "DB_DRIVER": "sqlite"}
```

```toml
# .wt-helper.vars.toml
PORT = "8080"
DB_DRIVER = "sqlite"
```

```yaml
# .wt-helper.vars.yaml
PORT: "8080"
DB_DRIVER: "sqlite"
```

### Dependency directories (symlink/copy into worktrees)

Some directories are `.gitignored` but needed in every worktree — build caches (`.go`, `.npm`), dependencies (`node_modules`, `vendor`), etc. `wt-helper` can symlink or deep-copy them automatically.

```bash
# Symlink .go and node_modules into every new worktree
wt-helper setup --envrc-file .envrc --dep-dir .go --dep-dir node_modules

# Deep copy instead of symlink (for dirs that break with symlinks)
wt-helper setup --envrc-file .envrc --dep-dir .go --dep-dir node_modules --dep-copy

# Interactively pick which dirs to link (tab-completion available)
wt-helper setup --envrc-file .envrc --dep-interactive
```

At worktree creation, the hook creates the symlinks/copies AFTER installing `.envrc` and BEFORE `direnv allow`. If the directory already exists in the worktree, it is overwritten.

Completion for `--dep-dir` lists directories in the target repo:

```bash
wt-helper setup --dep-dir <TAB>
# → .go  node_modules  vendor  .cache
```

### The happy path

```bash
# Template repos — interactive creation, prompts for vars
git wt-add ../my-feature -b feature-branch
#   PORT [8080] =          ← press Enter to keep default
#   DB_DRIVER [sqlite] =   ← press Enter to keep default
# → prepares vars, creates worktree, renders .envrc, direnv allow

# Override vars via env — skipped from prompts
SECRET=agent-42 git wt-add ../my-auth -b auth-work
#   PORT [8080] =          ← only PORT prompts, SECRET skipped

# Plain repos — .envrc copied verbatim, no prompts
git worktree add ../my-plain -b plain-branch
# → wt-helper: .envrc installed and allowed

# Work on your feature...

# Clean up — wrapup runs, worktree goes away
git wt-remove ../my-feature
# → wt-helper: running wrapup for .../my-feature...
# → (docker stops, databases drop, angels weep)
# → wt-helper: worktree removed

# Force delete when git complains about untracked files
# (we've all been there)
git wt-remove ../my-feature --force
```

### Shell completion

Because tab-completing is the closest most of us will get to telekinesis:

```bash
# bash
wt-helper completion bash > /etc/bash_completion.d/wt-helper

# zsh
wt-helper completion zsh > "${fpath[1]}/_wt-helper"

# fish
wt-helper completion fish > ~/.config/fish/completions/wt-helper.fish
```

## How it works

### Setup

`wt-helper setup` makes the repo self-contained:

1. Copies your `.envrc` (or template) into `.git/envrc-source`
2. If template: copies your vars file into `.git/worktree-helper-vars.{json,toml,yaml}`
3. If template: renders a default `.envrc` in the repo root (git-ignored)
4. Writes config to `.git/worktree-helper.conf`
5. Installs a `post-checkout` hook
6. Installs a `git wt-remove` alias
7. If template: installs a `git wt-add` alias
8. If `--dep-dir` specified: stores dependency dirs in config for linking/copying into new worktrees

### Interactive creation: `git wt-add` alias (template mode only)

For template repos, `git wt-add` handles vars BEFORE spawning the worktree:

1. Reads the default vars file from `.git/worktree-helper-vars.{format}`
2. Prompts for ALL custom vars, showing defaults in brackets
3. Any env var already set (`PORT=9090`) skips the prompt for that var
4. Writes merged vars to a temp file, sets `WT_VARS_FILE` env var
5. Runs `git worktree add` → triggers the hook below
6. Cleans up the temp file

### Creation: `post-checkout` hook (engine)

Triggered by ANY `git worktree add` (or `git wt-add`). The hook:

1. Detects new worktrees (null SHA trick — don't ask)
2. Reads `.git/worktree-helper.conf` to decide what to do
3. Checks `$WT_VARS_FILE` — if set by `git wt-add`, uses it instead of the default vars file
4. If `is-template = true`: calls `wt-helper render-template` with the vars file
5. If `is-template = false`: copies `.git/envrc-source` verbatim
6. Creates symlinks (or deep copies) for configured dependency dirs
7. Runs `direnv allow` so you don't have to type it like an animal
8. If the worktree already has an `.envrc`, asks before overwriting (it's polite like that)

The hook uses marker comments (`# >>> wt-helper start` / `# <<< wt-helper end`) so re-running `setup` doesn't clobber your other hooks. It's surgical. It's precise. It's better than you at managing hooks.

### Deletion: `git wt-remove` alias

Git has no hook for worktree deletion, which is rude. So wt-helper installs a `git wt-remove` alias that:

1. Reads your wrapup command from `.git/worktree-helper.conf`
2. Runs it through `bash -c` (sources your `~/.profile` so shell functions work)
3. If the wrapup fails, aborts — your cleanup had one job
4. Runs `git worktree remove` and reports back
5. Forwards extra flags (`--force`, `-f`) to `git worktree remove`

### What can `--wrapup-cmd` be?

| What you pass | What happens | Example |
|---|---|---|
| A file path | Validated at setup (must exist + be executable) | `./scripts/cleanup.sh` |
| A command name | Stored as-is, resolved at runtime via PATH | `docker` |
| A shell function | Stored as-is, resolved via `~/.profile` at runtime | `juju-kill-all` |

The wrapup command receives the worktree absolute path as `$1`. Use it however you want. We don't judge.

## Configuration

Setup writes `.git/worktree-helper.conf`:

```ini
# Generated by wt-helper — do not edit manually
main-repo = /home/you/repos/my-project
envrc-source = /home/you/repos/my-project/.git/envrc-source
wrapup-cmd = ./scripts/cleanup.sh
vars-file = /home/you/repos/my-project/.git/worktree-helper-vars.json
helper-bin = /home/you/.local/bin/wt-helper
is-template = true
```

You can peek at it. You can even edit it. We said "do not edit" but we're not your parents.

### Repo structure after setup

```
my-repo/
├── .envrc                    # rendered default (git-ignored)
├── .gitignore                # updated to include .envrc
├── .git/
│   ├── envrc-source          # your template or plain .envrc
│   ├── worktree-helper.conf  # config
│   └── worktree-helper-vars.json  # your vars (if template)
└── ...
```

## Examples

### .envrc (plain copy)

```bash
# Each worktree gets the same .envrc, shell variables resolve at direnv load time
export PORT=${PORT:-8080}
export DATABASE_URL="sqlite:///tmp/$(basename $PWD).db"
export GOPATH="$PWD/.go"
export PROJECT_NAME="$(basename $PWD)"
```

### .envrc template (per-worktree values)

```bash
# .envrc.template — rendered per worktree with Go templates
export PORT={{.PORT}}
export DATABASE_URL="{{.DB_DRIVER}}:///tmp/{{.WorktreeName}}.db"
export WORKTREE={{.WorktreeName}}
export BRANCH={{.BranchName}}
export GOPATH="{{.WorktreePath}}/.go"
```

With this vars file:
```json
{"PORT": "8080", "DB_DRIVER": "sqlite"}
```

Agent 1 creates a worktree:
```bash
git wt-add ../agent-1 -b feature-auth
#   PORT [8080] =        press Enter
#   DB_DRIVER [sqlite] = postgres         type override
```

Agent 1's rendered `.envrc`:
```bash
export PORT=8080
export DATABASE_URL="postgres:///tmp/agent-1.db"
export WORKTREE=agent-1
export BRANCH=feature-auth
```

Agent 2 passes vars via env:
```bash
PORT=9090 git wt-add ../agent-2 -b fix-bug
#   DB_DRIVER [sqlite] =           press Enter (PORT skipped — in env)
```

Agent 2's rendered `.envrc`:
```bash
export PORT=9090
export DATABASE_URL="sqlite:///tmp/agent-2.db"
export WORKTREE=agent-2
export BRANCH=fix-bug
```

Same template, different worktrees. No port conflicts, no shared state, no therapist bills.

### Cleanup script

```bash
#!/bin/sh
# scripts/cleanup.sh — called with worktree path as $1
WORKTREE="$1"
NAME=$(basename "$WORKTREE")

echo "Cleaning up $NAME..."
docker stop "dev-$NAME" 2>/dev/null
docker rm "dev-$NAME" 2>/dev/null
rm -f "/tmp/$NAME.db"
echo "$NAME is gone. Like it never existed."
```

### Shell function wrapup

```bash
# In ~/.profile — because sometimes your cleanup IS a shell function
function juju-kill-all() {
    juju controllers --format=json | jq -r '.controllers | keys[]' | xargs -I{} juju kill-controller --no-prompt {}
}
```

```bash
wt-helper setup --wrapup-cmd juju-kill-all
# Now: git wt-remove tears down all juju controllers before removing the worktree
# Use responsibly. Or don't. We're not your conscience.
```

## FAQ

**Q: Why not just use `git worktree add` and copy `.envrc` manually?**
A: Why wash dishes manually when you have a dishwasher?

**Q: What's the difference between `git wt-add` and `git worktree add`?**
A: `git wt-add` prompts you for template vars BEFORE creating the worktree (template mode only). `git worktree add` skips prompting — the hook uses the default vars file and any env var overrides. Both trigger the same `post-checkout` hook.

**Q: What happens if I run `setup` twice?**
A: It updates the hook in place (markers, remember?). No duplicates. It's idempotent, like a well-behaved function should be.

**Q: Can I use this with my existing `post-checkout` hook?**
A: Yes. wt-helper appends its section between markers. Your existing hook content is preserved. Two hooks, one file, no drama.

**Q: `git wt-remove` says "wrapup failed" — what gives?**
A: Your wrapup command exited non-zero. Fix your script, or the worktree stays. wt-helper has trust issues, and that's a feature.

**Q: Does this work on macOS?**
A: Probably. We haven't tested it. If it doesn't, open an issue and we'll pretend to be surprised.

**Q: Can I use templates with shell variables like `$(basename $PWD)` instead?**
A: Yes, that's the "plain copy" mode. If your `.envrc` doesn't contain `{{`, it's copied verbatim and shell variables are evaluated by direnv at load time. Templates are for when you need values resolved at worktree creation time (e.g., reading from a config file).

**Q: What if I have both a `.wt-helper.vars.json` and a `.wt-helper.vars.yaml` in my repo?**
A: JSON wins. The detection order is: `.json` > `.toml` > `.yaml` > `.yml`. Or just use `--vars-file` explicitly and stop confusing the tool.

**Q: How do env var overrides work?**
A: Any env var matching a template variable name skips the interactive prompt in `git wt-add`. The env var value is used automatically. For plain `git worktree add`, the `post-checkout` hook overlays env vars when calling `render-template`.
