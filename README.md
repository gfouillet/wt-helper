# wt-helper

**Because `git worktree add` should be smarter than you are at 3am.**

A CLI tool that makes git worktrees actually usable for parallel agent workflows. It copies your `.envrc` when you spawn a worktree and cleans up your mess when you delete one.

## What it does

```
git worktree add → "here's your .envrc, I already direnv-allow'd it, you're welcome"
git wt-remove    → "let me run your cleanup script before I nuke this"
```

No more forgetting to copy `.envrc`. No more orphaned docker containers named `dev-feature-branch-42` haunting your machine like digital ghosts.

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
# The full monty
wt-helper setup --envrc-file .envrc --wrapup-cmd ./scripts/cleanup.sh

# Just envrc (I live alone, I don't need cleanup)
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

**Env var overrides at worktree creation time** — because sometimes one agent needs a different port:

```bash
# Default: uses vars file values
git worktree add ../feature -b feature
# → PORT=8080

# Override: env var takes precedence
PORT=9090 git worktree add ../feature -b feature
# → PORT=9090

# Multiple overrides
PORT=7070 DB_DRIVER=postgres git worktree add ../feature -b feature
# → PORT=7070, DB_DRIVER=postgres
```

### The happy path

```bash
# Spawn a worktree — .envrc appears like magic
git worktree add ../my-feature -b feature-branch
# → wt-helper: .envrc installed and allowed
# → (you didn't have to do anything, you're welcome)

# Work on your feature...

# Clean up — wrapup runs, worktree goes away
git wt-remove ../my-feature
# → wt-helper: running wrapup for /home/you/repos/../my-feature...
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

### Creation: `post-checkout` hook

When you `git worktree add`, Git fires a `post-checkout` hook. The hook:

1. Detects "oh, this is a fresh worktree" (null SHA trick — don't ask)
2. Reads `.git/worktree-helper.conf` to decide what to do
3. If `is-template = true`: calls `wt-helper render-template` with the vars file + any env var overrides
4. If `is-template = false`: copies the source `.envrc` verbatim
5. Runs `direnv allow` so you don't have to type it like an animal
6. If the worktree already has an `.envrc`, asks before overwriting (it's polite like that)

The hook reads config at runtime, so changing `is-template` in the config file takes effect immediately — no need to re-run setup.

The hook uses marker comments (`# >>> wt-helper start` / `# <<< wt-helper end`) so re-running `setup` doesn't clobber your other hooks. It's surgical. It's precise. It's better than you at managing hooks.

### Deletion: `git wt-remove` alias

Git has no hook for worktree deletion, which is rude. So wt-helper installs a `git wt-remove` alias that:

1. Reads your wrapup command from `.git/worktree-helper.conf`
2. Runs it through `bash -c` (sources your `~/.profile` so shell functions work)
3. If the wrapup fails, aborts — your cleanup had one job
4. Runs `git worktree remove` and reports back

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

Agent 1's worktree (`feature-auth`) gets:
```bash
export PORT=8080
export DATABASE_URL="sqlite:///tmp/feature-auth.db"
export WORKTREE=feature-auth
export BRANCH=feature-auth
```

Agent 2's worktree (`fix-bug`) gets:
```bash
export PORT=8080
export DATABASE_URL="sqlite:///tmp/fix-bug.db"
export WORKTREE=fix-bug
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

**Q: How do env var overrides work with templates?**
A: When a worktree is created, `wt-helper render-template` checks the process environment for any variable matching a template variable name. Env vars take precedence over the vars file. So `PORT=9090 git worktree add ...` gives that worktree PORT=9090 while others get the default from the vars file.
