//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"syscall"
)

func systemShutdown() error {
	return runShutdown("/s", "/t", "0", "/f")
}

func systemRestart() error {
	return runShutdown("/r", "/t", "0", "/f")
}

func runShutdown(args ...string) error {
	cmd := exec.Command("shutdown", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("shutdown %v: %v (%s)", args, err, string(out))
	}
	return nil
}
