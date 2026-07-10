// Package restart centralises the "re-execute the new binary after a
// self-update" decision so main.go doesn't have to know about systemd.
//
// In production the gokych backend runs as a systemd-managed service
// (User=deploy, /etc/systemd/system/gokych.service). The previous
// implementation used syscall.Exec to replace the process in-place:
// the PID stayed the same so systemd tracked the restart as a normal
// service cycle, but the new binary had to be re-execed by the same
// process — which the deploy user can't trigger if the process ever
// exited before the exec landed, and which the operator had to fall
// back to a manual `sudo systemctl restart gokych.service` when it
// didn't take. The systemd strategy here fixes that by asking systemd
// to restart the service from the outside (the deploy user only needs
// NOPASSWD sudo on `systemctl restart gokych.service`, which is far
// less privilege than a root shell or a setuid binary).
//
// Non-systemd environments (macOS dev, docker, manual `./gokych` runs)
// keep the old behaviour: syscall.Exec replaces the process image.
package restart

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// Strategy names the restart mechanism the running process should use.
type Strategy string

const (
	// StrategySystemd asks systemd to restart the gokych service via
	// `sudo systemctl restart gokych.service`. The process then exits
	// via the existing SIGTERM handler so systemd can bring the new
	// binary up cleanly.
	StrategySystemd Strategy = "systemd"
	// StrategyExec replaces the current process in place with the new
	// binary via syscall.Exec. Used in dev / non-systemd environments.
	StrategyExec Strategy = "exec"
)

// goos / execCommand / execLookPath are package-level so tests can swap
// them out without touching the real runtime.
var (
	goos         = runtime.GOOS
	execCommand  = exec.Command
	execLookPath = exec.LookPath
)

// PickStrategy returns the restart mechanism appropriate for the current
// host. The check is deliberately conservative: anything we can't
// positively confirm is treated as a non-systemd environment so the
// fallback to syscall.Exec stays safe.
func PickStrategy() Strategy {
	if goos != "linux" {
		return StrategyExec
	}
	// INVOCATION_ID is set by systemd for every unit's main process; the
	// presence of this env var is the most reliable "I was started by
	// systemd" signal we have. JOURNAL_STREAM is a backup that older
	// systemd releases set, but INVOCATION_ID alone is enough.
	if os.Getenv("INVOCATION_ID") == "" {
		return StrategyExec
	}
	if _, err := execLookPath("systemctl"); err != nil {
		return StrategyExec
	}
	if _, err := execLookPath("sudo"); err != nil {
		return StrategyExec
	}
	return StrategySystemd
}

// SystemdRestart spawns `sudo systemctl restart <serviceName>` in a
// detached process group and returns immediately. The caller is
// expected to exit shortly after (systemd will SIGTERM the current
// process, the graceful shutdown handler in main runs, and the new
// instance starts). The systemctl call does NOT block this process
// because it lives in a separate process group; systemd waiting for
// the old PID to exit is unrelated to us.
func SystemdRestart(serviceName string) error {
	sudoPath, err := execLookPath("sudo")
	if err != nil {
		return fmt.Errorf("sudo not found: %w", err)
	}
	systemctlPath, err := execLookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl not found: %w", err)
	}
	cmd := execCommand(sudoPath, systemctlPath, "restart", serviceName)
	// Detach: if our process exits, the systemctl child must keep
	// running so it can complete the restart handshake with systemd.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start systemctl: %w", err)
	}
	// Reap the child asynchronously to avoid a zombie. Errors are
	// non-fatal: the restart has already been initiated by the time
	// Start() returns.
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Warn("restart: systemctl exit", "service", serviceName, "err", err)
		}
	}()
	return nil
}

// ExecRestart replaces the current process with the new binary via
// syscall.Exec. The PID is preserved so the parent process manager
// (systemd, or just the shell in dev) sees a continuous service.
//
// exePath is the absolute path of the new binary; args / env are
// forwarded unchanged. Callers should shut down any open resources
// (HTTP server, DB pool, worker pools) before calling — syscall.Exec
// does not run deferred functions.
func ExecRestart(exePath string, args []string, env []string) error {
	return syscall.Exec(exePath, args, env)
}
