package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/donovan-yohan/crag/internal/config"
	"github.com/donovan-yohan/crag/internal/lima"
	"github.com/donovan-yohan/crag/internal/session"
)

type Request struct {
	// Source is either a local filesystem path or a git URL.
	Source string
	// Task is the natural-language description handed to belayer.
	Task string
}

type Dispatcher struct {
	cfg *config.Config
	vm  *lima.VM
}

func New(cfg *config.Config) *Dispatcher {
	return &Dispatcher{cfg: cfg, vm: lima.New(cfg.Lima.VMName)}
}

// Submit ensures the worker is up, syncs the workspace, kicks off belayer,
// and returns the new session id. It does not wait for the run to finish —
// pair with Wait for completion semantics.
func (d *Dispatcher) Submit(ctx context.Context, req Request) (string, error) {
	if err := d.vm.Start(ctx); err != nil {
		return "", fmt.Errorf("start vm: %w", err)
	}

	workdir, err := d.resolveWorkspace(ctx, req.Source)
	if err != nil {
		return "", err
	}

	id, err := session.New()
	if err != nil {
		return "", err
	}
	if err := d.vm.ShellScript(ctx, d.belayerCmd(id, "cd "+shellQuote(workdir)+" && ", "run", "start", "--task", req.Task)); err != nil {
		return "", fmt.Errorf("start belayer: %w", err)
	}
	if err := session.RecordLatest(id); err != nil {
		return "", fmt.Errorf("record session: %w", err)
	}
	return id, nil
}

// Wait polls Status until the session reports a terminal state. Repeated
// status lines are deduped so the user only sees transitions. A successful
// terminal state returns nil; a failure state returns an error so `crag run`
// exits non-zero. Transient Status errors retry a few times with backoff —
// limactl/SSH can hiccup, and a single blip shouldn't abort a long-running
// session. On context cancellation Wait fires a best-effort `belayer cancel`
// inside the VM, since killing limactl on the host doesn't propagate through
// SSH to the in-VM belayer process.
func (d *Dispatcher) Wait(ctx context.Context, sessionID string) error {
	const (
		pollInterval = 5 * time.Second
		maxRetries   = 3
	)

	defer func() {
		if ctx.Err() != nil {
			d.cancelInVM(sessionID)
		}
	}()

	var (
		last       string
		retries    int
		retryDelay = pollInterval
	)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := d.Status(ctx, sessionID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			retries++
			if retries > maxRetries {
				return fmt.Errorf("status check failed %d times in a row: %w", retries, err)
			}
			fmt.Fprintf(os.Stderr, "[%s] %s status check failed (attempt %d/%d): %v\n",
				time.Now().Format(time.RFC3339), sessionID, retries, maxRetries, err)
			if err := sleep(ctx, retryDelay); err != nil {
				return err
			}
			retryDelay += pollInterval
			continue
		}
		retries = 0
		retryDelay = pollInterval

		if status != last {
			fmt.Printf("[%s] %s %s\n", time.Now().Format(time.RFC3339), sessionID, status)
			last = status
		}
		switch classify(status) {
		case statusSucceeded:
			return nil
		case statusFailed:
			return fmt.Errorf("session %s ended with status: %s", sessionID, status)
		}
		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}
}

// sleep blocks for d or until ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// cancelInVM fires `belayer cancel <sessionID>` inside the VM using a fresh
// context so it runs even after the parent context is already cancelled.
// Best-effort: we have no useful response to a cancel failure beyond the
// log line.
func (d *Dispatcher) cancelInVM(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fmt.Fprintf(os.Stderr, "[%s] %s cancelling in-VM session...\n",
		time.Now().Format(time.RFC3339), sessionID)
	cmd := d.vm.Shell(ctx, "bash", "-lc", d.belayerCmd(sessionID, "", "cancel"))
	_ = cmd.Run()
}

// Status returns belayer's status output for the given session.
func (d *Dispatcher) Status(ctx context.Context, sessionID string) (string, error) {
	cmd := d.vm.Shell(ctx, "bash", "-lc", d.belayerCmd(sessionID, "", "status"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("belayer status: %w: %s", err, msg)
		}
		return "", fmt.Errorf("belayer status: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Logs streams `belayer logs --follow` output for the given session.
func (d *Dispatcher) Logs(ctx context.Context, sessionID string) error {
	return d.vm.ShellScript(ctx, d.belayerCmd(sessionID, "", "logs", "--follow"))
}

// VM exposes the underlying Lima wrapper for vm subcommands.
func (d *Dispatcher) VM() *lima.VM { return d.vm }

// belayerCmd assembles a bash one-liner that exports CRAG_SESSION and invokes
// belayer. prefix is inserted before the binary call (e.g. "cd /path && ").
//
// CRAG_SESSION is exported so a future belayer can pick up the id without a
// CLI flag change; today's belayer ignores it. See
// docs/design-docs/2026-04-16-worker-abstraction-and-remote-topology.md.
func (d *Dispatcher) belayerCmd(sessionID, prefix string, args ...string) string {
	parts := append([]string{d.cfg.Belayer.Binary}, args...)
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = shellQuote(p)
	}
	return fmt.Sprintf("%sCRAG_SESSION=%s %s", prefix, shellQuote(sessionID), strings.Join(quoted, " "))
}

// resolveWorkspace returns the in-VM path belayer should run in. Local paths
// under $HOME pass through (Lima auto-mounts $HOME). Git URLs are cloned
// into the configured workspace mount inside the VM.
func (d *Dispatcher) resolveWorkspace(ctx context.Context, source string) (string, error) {
	if isGitURL(source) {
		return d.cloneInVM(ctx, source)
	}

	abs, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	// Resolve symlinks on both sides before comparing — otherwise a
	// symlinked workspace under $HOME pointing outside it (or a subtle
	// prefix collision like /Users/alice-evil under /Users/alice) sneaks
	// past a naive HasPrefix check.
	absReal, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	homeReal, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	sep := string(os.PathSeparator)
	if absReal != homeReal && !strings.HasPrefix(absReal, homeReal+sep) {
		return "", fmt.Errorf("local path %s resolves to %s, outside $HOME (%s); lima only auto-mounts $HOME", abs, absReal, homeReal)
	}
	return absReal, nil
}

func (d *Dispatcher) cloneInVM(ctx context.Context, source string) (string, error) {
	mount := d.cfg.Belayer.WorkspaceMount
	target := path.Join(mount, repoName(source))

	script := fmt.Sprintf(`
set -euo pipefail
mkdir -p %[1]s
if [ -d %[2]s/.git ]; then
  git -C %[2]s fetch --all --prune
  git -C %[2]s reset --hard origin/HEAD
else
  rm -rf %[2]s
  git clone %[3]s %[2]s
fi
`, shellQuote(mount), shellQuote(target), shellQuote(source))

	if err := d.vm.ShellScript(ctx, script); err != nil {
		return "", err
	}
	return target, nil
}

func isGitURL(s string) bool {
	switch {
	case strings.HasPrefix(s, "git@"),
		strings.HasPrefix(s, "ssh://"):
		return true
	case strings.HasPrefix(s, "https://") && strings.HasSuffix(s, ".git"):
		return true
	}
	return false
}

func repoName(source string) string {
	return strings.TrimSuffix(path.Base(source), ".git")
}

type statusKind int

const (
	statusActive statusKind = iota
	statusSucceeded
	statusFailed
)

var (
	successStatuses = map[string]bool{
		"completed": true,
		"complete":  true,
		"finished":  true,
	}
	failureStatuses = map[string]bool{
		"failed":    true,
		"errored":   true,
		"cancelled": true,
		"canceled":  true,
	}
)

// classify assumes belayer `status` emits a single canonical status token.
// Only an exact match (after lowercase + trim) is treated as terminal —
// prose like "running (last completed step: clone)" stays active. This is
// conservative: if belayer's output format ever changes we loop until the
// user Ctrl-Cs, which is preferable to misreporting success or failure.
func classify(status string) statusKind {
	token := strings.TrimSpace(strings.ToLower(status))
	switch {
	case successStatuses[token]:
		return statusSucceeded
	case failureStatuses[token]:
		return statusFailed
	default:
		return statusActive
	}
}

// shellQuote wraps s in single quotes for safe inclusion in a bash command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
