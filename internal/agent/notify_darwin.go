//go:build darwin

package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

func showTeacherMessage(text string, allowReply bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty message")
	}

	var script string
	if allowReply {
		script = fmt.Sprintf(`
try
  set dlg to display dialog %s with title "Proctor 教师消息" default answer "" buttons {"知道了", "发送回复"} default button "知道了" with icon note
  set btn to button returned of dlg
  set ans to text returned of dlg
  if btn is "发送回复" and (ans as string) is not "" then
    return "reply: " & ans
  else
    return "acked"
  end if
on error number -128
  return "dismissed"
end try
`, appleString(text))
	} else {
		script = fmt.Sprintf(`
try
  display dialog %s with title "Proctor 教师消息" buttons {"知道了"} default button 1 with icon note
  return "acked"
on error number -128
  return "dismissed"
end try
`, appleString(text))
	}

	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("osascript: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return normalizeMessageResult(string(out)), nil
}

func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
