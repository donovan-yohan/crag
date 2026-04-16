# crag

Lightweight outer control plane for Nightshift local dev. `crag` runs on macOS,
dispatches belayer runs to a Lima VM worker, and polls/streams results back to
your terminal.

This is **proof infrastructure**, not a product. No daemon, no database, no
clamshell integration yet — just a CLI that wraps `limactl` and the `belayer`
CLI inside the VM.

## Architecture

```
┌─ macOS ────────────┐         ┌─ Lima VM (devbox) ─────────────┐
│                    │         │                                │
│  crag run …        │ shell   │  belayer run start …           │
│  crag status       ├────────►│  belayer status                │
│  crag logs         │         │  belayer logs --follow         │
│                    │         │                                │
└────────────────────┘         └────────────────────────────────┘
```

`crag` itself does **not** import any belayer packages. It shells out to
`belayer` inside the VM via `limactl shell devbox -- …`.

## Install

```bash
go install github.com/donovan-yohan/crag/cmd/crag@latest
```

Or build locally:

```bash
go build -o ./bin/crag ./cmd/crag
```

Prerequisites:

- macOS with [Lima](https://lima-vm.io) installed (`brew install lima`)
- A Lima VM (default name `devbox`) with `belayer` already on its `PATH`

## Configuration

On first run, crag writes `~/.crag/config.yaml` with sensible defaults:

```yaml
lima:
  vm_name: devbox
belayer:
  socket_path: /run/user/1000/belayer/belayer.sock
  workspace_mount: /var/tmp/crag-workspaces
  binary: belayer
```

Edit it to point at a different VM or belayer install.

## Usage

```bash
# Submit a run against a local checkout and wait for it to finish (local paths
# must live under $HOME — Lima auto-mounts $HOME into the VM at the same path).
crag run ~/Documents/Programs/personal/arielcharts \
  --task "Add a dark mode toggle"

# Or against a git URL — crag clones it inside the VM under workspace_mount.
crag run https://github.com/donovan-yohan/arielcharts.git \
  --task "Add a dark mode toggle"

# Submit and exit immediately, without waiting. Prints the session id.
crag run ~/path/to/repo --task "..." --detach

# Poll status. Pass an explicit session id, or omit to use the most recent.
crag status                       # latest session
crag status 20260416-153012-a3f4c8

# Stream session logs.
crag logs                         # latest session
crag logs 20260416-153012-a3f4c8

# Manage the VM directly.
crag vm start
crag vm stop
```

### Session ids

Every `crag run` mints a session id (timestamp + random suffix) and stores it
at `~/.crag/last-session`. `status` and `logs` default to that id when called
without an argument — pass an explicit id once we start juggling multiple
in-flight sessions.

## Layout

```
cmd/crag/main.go              cobra entrypoint (run/status/logs/vm)
internal/config/config.go     YAML loader for ~/.crag/config.yaml
internal/lima/lima.go         limactl wrapper (Start/Stop/Shell/IsRunning)
internal/dispatch/dispatch.go orchestration (Submit/Wait/Status/Logs)
internal/session/session.go   session-id minting and last-session tracking
docs/design-docs/             tradeoffs and deferred decisions
```

## What's not here yet

- Request queue / persistence (single in-flight run only)
- Multi-repo workspace definitions
- Clamshell sandbox integration
- Anything resembling a daemon or web UI

These belong in later phases — see Phase 4+ of `docs/design-docs/2026-04-16-sandbox-runtime-and-crag-proof.md` in the belayer repo.
