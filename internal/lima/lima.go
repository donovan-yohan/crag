package lima

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

type VM struct {
	Name string
}

func New(name string) *VM {
	return &VM{Name: name}
}

func (v *VM) IsRunning(ctx context.Context) (bool, error) {
	out, err := exec.CommandContext(ctx, "limactl", "list", "--format", "{{.Status}}", v.Name).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "Running", nil
}

func (v *VM) Start(ctx context.Context) error {
	running, err := v.IsRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	cmd := exec.CommandContext(ctx, "limactl", "start", v.Name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v *VM) Stop(ctx context.Context) error {
	running, err := v.IsRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	cmd := exec.CommandContext(ctx, "limactl", "stop", v.Name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Shell prepares an exec.Cmd that runs args inside the VM via `limactl shell`.
// The caller wires stdin/stdout/stderr and chooses Run/Output/Start.
func (v *VM) Shell(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"shell", v.Name, "--"}, args...)
	return exec.CommandContext(ctx, "limactl", full...)
}

// ShellScript runs a multi-line bash script inside the VM and streams output
// to the local stdout/stderr.
func (v *VM) ShellScript(ctx context.Context, script string) error {
	cmd := v.Shell(ctx, "bash", "-lc", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
