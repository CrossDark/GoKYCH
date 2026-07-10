package restart

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// withHooks swaps the package-level hooks for the duration of fn, then
// restores them. The original goos / command / lookup funcs are captured
// in closures so concurrent test runs don't fight over the package
// globals.
func withHooks(t *testing.T, fn func()) {
	t.Helper()
	origGOOS := goos
	origCmd := execCommand
	origLook := execLookPath
	t.Cleanup(func() {
		goos = origGOOS
		execCommand = origCmd
		execLookPath = origLook
	})
	fn()
}

func TestPickStrategy(t *testing.T) {
	withHooks(t, func() {
		t.Run("non-linux returns exec", func(t *testing.T) {
			goos = "darwin"
			os.Unsetenv("INVOCATION_ID")
			if got := PickStrategy(); got != StrategyExec {
				t.Errorf("PickStrategy() = %v, want %v", got, StrategyExec)
			}
		})

		t.Run("linux without INVOCATION_ID returns exec", func(t *testing.T) {
			goos = "linux"
			os.Unsetenv("INVOCATION_ID")
			if got := PickStrategy(); got != StrategyExec {
				t.Errorf("PickStrategy() = %v, want %v", got, StrategyExec)
			}
		})

		t.Run("linux with INVOCATION_ID but no systemctl returns exec", func(t *testing.T) {
			goos = "linux"
			os.Setenv("INVOCATION_ID", "test-invocation")
			t.Cleanup(func() { os.Unsetenv("INVOCATION_ID") })
			execLookPath = func(name string) (string, error) {
				if name == "systemctl" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + name, nil
			}
			if got := PickStrategy(); got != StrategyExec {
				t.Errorf("PickStrategy() = %v, want %v", got, StrategyExec)
			}
		})

		t.Run("linux with INVOCATION_ID but no sudo returns exec", func(t *testing.T) {
			goos = "linux"
			os.Setenv("INVOCATION_ID", "test-invocation")
			t.Cleanup(func() { os.Unsetenv("INVOCATION_ID") })
			execLookPath = func(name string) (string, error) {
				if name == "sudo" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/" + name, nil
			}
			if got := PickStrategy(); got != StrategyExec {
				t.Errorf("PickStrategy() = %v, want %v", got, StrategyExec)
			}
		})

		t.Run("linux with INVOCATION_ID + sudo + systemctl returns systemd", func(t *testing.T) {
			goos = "linux"
			os.Setenv("INVOCATION_ID", "test-invocation")
			t.Cleanup(func() { os.Unsetenv("INVOCATION_ID") })
			execLookPath = func(name string) (string, error) {
				switch name {
				case "systemctl":
					return "/usr/bin/systemctl", nil
				case "sudo":
					return "/usr/bin/sudo", nil
				}
				return "", exec.ErrNotFound
			}
			if got := PickStrategy(); got != StrategySystemd {
				t.Errorf("PickStrategy() = %v, want %v", got, StrategySystemd)
			}
		})
	})
}

func TestSystemdRestartBuildsCommand(t *testing.T) {
	withHooks(t, func() {
		execLookPath = func(name string) (string, error) {
			switch name {
			case "systemctl":
				return "/usr/bin/systemctl", nil
			case "sudo":
				return "/usr/bin/sudo", nil
			}
			return "", exec.ErrNotFound
		}
		// Capture the exec.Command call instead of actually starting
		// a real systemctl (this runs on developer macs and CI which
		// have neither systemd nor a gokych service to restart).
		var captured []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			captured = append([]string{name}, args...)
			// Return a real *exec.Cmd that just exits 0 immediately so
			// cmd.Start() and the async cmd.Wait() both succeed.
			return exec.Command("true")
		}
		if err := SystemdRestart("gokych.service"); err != nil {
			t.Fatalf("SystemdRestart: %v", err)
		}
		want := []string{"/usr/bin/sudo", "/usr/bin/systemctl", "restart", "gokych.service"}
		if strings.Join(captured, " ") != strings.Join(want, " ") {
			t.Errorf("command = %v, want %v", captured, want)
		}
	})
}

func TestSystemdRestartSurfacesMissingTools(t *testing.T) {
	withHooks(t, func() {
		execLookPath = func(name string) (string, error) {
			return "", exec.ErrNotFound
		}
		if err := SystemdRestart("gokych.service"); err == nil {
			t.Fatal("SystemdRestart should error when sudo/systemctl are missing")
		}
	})
}
