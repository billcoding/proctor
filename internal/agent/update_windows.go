//go:build windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// replaceExecutable stages the new binary. On Windows a running EXE can usually
// be renamed; we then move .new into place. If rename of the live file fails,
// a delayed batch script finishes the swap after process exit.
func replaceExecutable(targetPath, newBin string) error {
	mode := os.FileMode(0o755)
	if st, err := os.Stat(targetPath); err == nil {
		mode = st.Mode().Perm()
	}
	newPath := targetPath + ".new"
	bakPath := targetPath + ".bak"
	_ = os.Remove(newPath)
	if err := copyFile(newBin, newPath, mode); err != nil {
		return fmt.Errorf("stage .new: %w", err)
	}
	_ = os.Remove(bakPath)
	if err := os.Rename(targetPath, bakPath); err != nil {
		if err2 := writePendingReplaceScript(targetPath, newPath, bakPath); err2 != nil {
			return fmt.Errorf("rename live exe failed (%v) and could not write delay script: %w", err, err2)
		}
		return nil
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		_ = os.Rename(bakPath, targetPath)
		_ = os.Remove(newPath)
		return fmt.Errorf("activate new binary: %w", err)
	}
	return nil
}

func writePendingReplaceScript(target, newPath, bakPath string) error {
	script := target + ".update.bat"
	content := fmt.Sprintf(
		"@echo off\r\n"+
			"timeout /t 2 /nobreak >nul\r\n"+
			"if exist \"%s\" del /f /q \"%s\"\r\n"+
			"if exist \"%s\" move /y \"%s\" \"%s\"\r\n"+
			"if exist \"%s\" move /y \"%s\" \"%s\"\r\n"+
			"\"%s\" restart\r\n"+
			"del \"%%~f0\"\r\n",
		bakPath, bakPath,
		target, target, bakPath,
		newPath, newPath, target,
		target,
	)
	return os.WriteFile(script, []byte(content), 0o755)
}

func restartAfterUpdate(exePath, configPath string) error {
	const detachedProcess = 0x00000008

	script := exePath + ".update.bat"
	if _, err := os.Stat(script); err == nil {
		cmd := exec.Command("cmd", "/C", "start", "", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
		return nil
	}

	args := []string{"restart"}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	cmd := exec.Command(exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess}
	if err := cmd.Start(); err != nil {
		fullArgs := append([]string{"/C", "start", "", exePath}, args...)
		cmd2 := exec.Command("cmd", fullArgs...)
		cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err2 := cmd2.Start(); err2 != nil {
			return fmt.Errorf("restart: %v; fallback: %w", err, err2)
		}
		_ = cmd2.Process.Release()
		os.Exit(0)
		return nil
	}
	_ = cmd.Process.Release()
	os.Exit(0)
	return nil
}
