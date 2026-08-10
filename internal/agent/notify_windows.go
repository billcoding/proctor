//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func showTeacherMessage(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message")
	}
	// MessageBox via PowerShell — works without extra deps on classroom images.
	ps := fmt.Sprintf(`Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show(%s, 'Proctor 教师消息') | Out-Null`, psQuote(text))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell messagebox: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
