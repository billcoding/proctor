//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func showTeacherMessage(text string, allowReply bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty message")
	}

	display := firstEnv("DISPLAY", ":0")
	env := append(os.Environ(), "DISPLAY="+display)

	if allowReply {
		if path, err := exec.LookPath("zenity"); err == nil {
			prompt := text + "\n\n可输入简短回复后点「确定」；留空表示仅确认已读。"
			cmd := exec.Command(path, "--entry", "--title=Proctor 教师消息", "--text="+prompt, "--width=420")
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Cancel / close → treat as dismissed ack, not a hard failure.
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					return "dismissed", nil
				}
				return "", fmt.Errorf("zenity: %v (%s)", err, strings.TrimSpace(string(out)))
			}
			reply := strings.TrimSpace(string(out))
			if reply == "" {
				return "acked", nil
			}
			return normalizeMessageResult("reply: " + reply), nil
		}
		if path, err := exec.LookPath("kdialog"); err == nil {
			cmd := exec.Command(path, "--title", "Proctor 教师消息", "--inputbox", text+"\n\n可输入简短回复；留空表示仅确认已读。")
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					return "dismissed", nil
				}
				return "", fmt.Errorf("kdialog: %v (%s)", err, strings.TrimSpace(string(out)))
			}
			reply := strings.TrimSpace(string(out))
			if reply == "" {
				return "acked", nil
			}
			return normalizeMessageResult("reply: " + reply), nil
		}
	}

	// Ack-only dialog (or reply tools unavailable).
	if path, err := exec.LookPath("zenity"); err == nil {
		cmd := exec.Command(path, "--info", "--title=Proctor 教师消息", "--text="+text, "--width=420", "--ok-label=知道了")
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				return "dismissed", nil
			}
			return "", fmt.Errorf("zenity: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return "acked", nil
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		cmd := exec.Command(path, "--title", "Proctor 教师消息", "--msgbox", text)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("kdialog: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return "acked", nil
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		body := text
		if allowReply {
			body = text + "（当前环境无法弹出回复框，仅通知）"
		}
		cmd := exec.Command(path, "-u", "critical", "Proctor 教师消息", body)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("notify-send: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return "shown", nil
	}
	return "", fmt.Errorf("no zenity/kdialog/notify-send available")
}

func firstEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
