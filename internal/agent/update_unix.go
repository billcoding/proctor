//go:build unix

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// replaceExecutable writes newBin over targetPath.
// Strategy: copy to .new → rename running binary to .bak → rename .new into place.
func replaceExecutable(targetPath, newBin string) error {
	mode := os.FileMode(0o755)
	if st, err := os.Stat(targetPath); err == nil {
		mode = st.Mode().Perm()
		if mode == 0 {
			mode = 0o755
		}
	}
	newPath := targetPath + ".new"
	bakPath := targetPath + ".bak"
	_ = os.Remove(newPath)
	if err := copyFile(newBin, newPath, mode); err != nil {
		return fmt.Errorf("stage .new: %w", err)
	}
	if err := os.Chmod(newPath, mode); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	// Drop previous backup so rename of live binary succeeds.
	_ = os.Remove(bakPath)
	if err := os.Rename(targetPath, bakPath); err != nil {
		// Some mounts disallow rename of a busy inode; try direct overwrite via rename .new.
		if err2 := os.Rename(newPath, targetPath); err2 != nil {
			_ = os.Remove(newPath)
			return fmt.Errorf("rename to bak: %v; fallback overwrite: %w", err, err2)
		}
		return nil
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		// Attempt rollback.
		_ = os.Rename(bakPath, targetPath)
		_ = os.Remove(newPath)
		return fmt.Errorf("activate new binary: %w", err)
	}
	return nil
}

func restartAfterUpdate(exePath, configPath string) error {
	args := []string{"restart"}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	cmd := exec.Command(exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		// Fallback: exit and let systemd/launchd KeepAlive / Restart=always bring us back.
		os.Exit(0)
		return err
	}
	_ = cmd.Process.Release()
	os.Exit(0)
	return nil
}
