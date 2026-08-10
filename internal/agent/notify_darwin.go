//go:build darwin

package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

func showTeacherMessage(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message")
	}
	script := fmt.Sprintf(`display dialog %s with title "Proctor 教师消息" buttons {"知道了"} default button 1 with icon note`, appleString(text))
	cmd := exec.Command("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("osascript: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
