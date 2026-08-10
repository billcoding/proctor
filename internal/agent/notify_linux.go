//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func showTeacherMessage(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message")
	}
	// Prefer dialog tools so the student actually sees the message.
	if path, err := exec.LookPath("zenity"); err == nil {
		cmd := exec.Command(path, "--info", "--title=Proctor 教师消息", "--text="+text, "--width=420")
		cmd.Env = append(os.Environ(), "DISPLAY="+firstEnv("DISPLAY", ":0"))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("zenity: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		cmd := exec.Command(path, "-u", "critical", "Proctor 教师消息", text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("notify-send: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("no zenity/notify-send available")
}

func firstEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
