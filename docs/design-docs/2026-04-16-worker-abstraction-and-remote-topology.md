---
status: current
created: 2026-04-16
---

# Worker Abstraction and Remote Topology

Two architectural questions surfaced during the Phase 4 scaffold of crag. This
note captures the tradeoffs and the deferred decisions so future-us doesn't
have to re-derive them.

## Question 1: Is crag locked to Lima?

The current scaffold ties `dispatch.Dispatcher` to `*lima.VM` directly. Every
call site that needs to start/stop/shell goes through that concrete type.

### Cost of decoupling now

Small. The surface is four methods (`Start`, `Stop`, `IsRunning`, `Shell` /
`ShellScript`). A `Worker` interface plus a factory keyed on
`config.worker.type` is roughly 50 lines of change:

```go
type Worker interface {
    EnsureReady(ctx context.Context) error
    Release(ctx context.Context) error
    Shell(ctx context.Context, args ...string) *exec.Cmd
    ShellScript(ctx context.Context, script string) error
}
```

`lima.VM` implements it. New implementations (`ssh.Worker`, `docker.Worker`,
cloud devboxes) plug in without touching `dispatch`.

### Cost of waiting

Interfaces extracted from a single implementation tend to leak that
implementation's assumptions. `limactl shell vm -- cmd…` and `ssh host cmd…`
are close enough that the leak risk is low, but `EnsureReady`/`Release` already
hint at the trickier case below — Lima's "start the VM" semantics don't map
cleanly onto an always-on SSH host or a cost-bearing cloud instance.

### Decision

**Defer until a second backend is concretely needed.** The design doc's
`worker.type` field in the workspace YAML reserves the seam. Extract the
interface the moment we add the second implementation, not before — that's
when we'll know which assumptions to factor out.

Trigger to revisit: the first time we need to dispatch to anything other than
local Lima.

## Question 2: Eventual topology — remote always-on linux box

The end-state runtime for crag is not a macOS CLI. It's an always-on linux
host that owns a pool of devbox workers (some local Lima-equivalents on that
host, some hosted in the cloud) and accepts requests from thin clients.

### What changes vs. the current scaffold

**Crag splits in half.**

- `cragctl` (client, runs anywhere): `submit`, `status`, `logs`, `cancel`.
  Talks to `cragd` over HTTP/gRPC. No `limactl` calls.
- `cragd` (daemon, runs on the always-on host): owns the queue, holds
  workspace definitions, manages the worker pool, dispatches, polls
  belayer, persists results.

Today's `dispatch.Dispatcher` is shaped like what `cragd` will eventually run.
Today's `cmd/crag/main.go` is shaped like the client side. They aren't
entangled — good — but `Run` currently blocks until the session terminates,
which is wrong for the remote case. It needs to become `Submit → request id`,
with status fetched separately via the existing `Status` call.

**`Worker` becomes a pool, not a singleton.**

Different worker types have wildly different lifecycles:

| Worker         | Bring-up      | Cost          | Lifecycle                  |
|----------------|---------------|---------------|----------------------------|
| Local Lima     | ~30s          | free          | start per session ok       |
| SSH (hosted)   | always-on     | fixed         | acquire/release from pool  |
| Cloud (EC2/Fly)| 1–3 min       | per-second    | provision on demand        |

This means `EnsureReady`/`Release` are heavier than they look. The cleaner
shape is probably:

```go
type Pool interface {
    Acquire(ctx context.Context, kind string) (Worker, error)
    Release(ctx context.Context, w Worker) error
}
```

Per-request VM management (today's `Start` on every `crag run`) becomes pool
acquisition with reuse and idle eviction.

### What does NOT change

- The belayer side of the boundary. `cragd` still shells out to `belayer` via
  `Worker.Shell` — no belayer imports, no protocol changes.
- The workspace resolution logic (`resolveWorkspace`, `cloneInVM`). These
  belong in `cragd` and are agnostic to the worker type.
- The polling / log-streaming primitives.

### Decision

**Don't build for the remote case yet.** The current PoC needs to prove the
dispatch loop end-to-end against Lima first (Phase 4 → Phase 5 in the belayer
design doc). Designing for the remote topology now risks the wrong
abstractions — we don't yet know which workers we'll actually run, what the
queue semantics need to be, or whether persistence wants SQLite vs Postgres.

But two cheap things to do *before* Phase 5 even though we're deferring the
split:

1. **Split `dispatch.Run` into `Submit` and `Wait`.** Synchronous
   poll-to-completion is convenient for the CLI but wrong for the daemon
   case. Decoupling them now keeps the daemon refactor from rewriting the
   orchestration core.
2. **Stop assuming "one in-flight session."** Today `Status`/`Logs` query
   "the" current run. Tag operations by session id from the start.

Trigger to revisit the full split: when we actually want to submit from a
machine that isn't the one running the worker.

## Summary

| Question                  | Decision           | Trigger to revisit                |
|---------------------------|--------------------|-----------------------------------|
| `Worker` interface        | defer              | second worker backend is needed   |
| Remote daemon topology    | defer              | submit-from-elsewhere is needed   |
| `Submit`/`Wait` split     | done (2026-04-16)  | —                                 |
| Session id everywhere     | done (2026-04-16)  | —                                 |

### How "done" looks today

- `dispatch.Submit(ctx, req) → (sessionID, error)` registers the run and
  returns immediately. `dispatch.Wait(ctx, id)` polls until terminal. The CLI
  composes them by default; `crag run --detach` skips the wait.
- `Status` and `Logs` take a `sessionID` argument all the way down. The id is
  exported into the VM-side shell as `CRAG_SESSION` so a future belayer can
  pick it up without a CLI flag change. Today's belayer ignores it; that's
  fine — the crag-side surface is what matters for the daemon split.
- `internal/session` mints ids and persists "the most recent" at
  `~/.crag/last-session` so CLI ergonomics survive without a real registry.
  When the daemon lands, this file goes away in favor of an actual session
  store.
