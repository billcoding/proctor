//go:build !windows

package agent

import (
	"fmt"
	"os/exec"
	"runtime"
)

func systemShutdown() error {
	switch runtime.GOOS {
	case "darwin":
		return runPower("shutdown", "-h", "now")
	default:
		if err := runPower("systemctl", "poweroff"); err == nil {
			return nil
		}
		return runPower("shutdown", "-h", "now")
	}
}

func systemRestart() error {
	switch runtime.GOOS {
	case "darwin":
		return runPower("shutdown", "-r", "now")
	default:
		if err := runPower("systemctl", "reboot"); err == nil {
			return nil
		}
		return runPower("shutdown", "-r", "now")
	}
}

func runPower(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v (%s)", name, args, err, string(out))
	}
	return nil
}
