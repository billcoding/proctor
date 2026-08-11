//go:build windows

package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mbOK              = 0x00000000
	mbYesNo           = 0x00000004
	mbIconInformation = 0x00000040
	mbSetForeground   = 0x00010000
	mbTopmost         = 0x00040000
	mbDefButton2      = 0x00000100 // default = No / 否 = 知道了

	idYes = 6

	waitTimeout    = 0x00000102
	dialogTimeout  = 5 * time.Minute
	wtsWaitSeconds = 300
)

var (
	modWtsapi32         = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSSendMessageW = modWtsapi32.NewProc("WTSSendMessageW")
)

func showTeacherMessage(text string, allowReply bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty message")
	}

	// Prefer a real WinForms dialog in the interactive user session (works from Session 0).
	if out, err := showReplyDialogInUserSession(text, allowReply); err == nil {
		return normalizeMessageResult(out), nil
	} else if currentProcessSessionID() != 0 {
		if out, err2 := showReplyDialogPowerShell(text, allowReply); err2 == nil {
			return normalizeMessageResult(out), nil
		}
	}

	// Fallback: WTSSendMessage buttons (no free-text). Chinese Windows shows 是/否.
	title := "Proctor 教师消息"
	body := text
	if allowReply {
		body = text + "\n\n点击「是」可继续输入回复；点击「否」表示知道了。"
		resp, err := sendWTSMessageToActiveSessions(title, body, mbYesNo|mbIconInformation|mbSetForeground|mbTopmost|mbDefButton2, true)
		if err != nil {
			return "", err
		}
		if resp == idYes {
			if reply, err := promptReplyInUserSession(""); err == nil {
				reply = strings.TrimSpace(reply)
				if reply != "" {
					return normalizeMessageResult("reply: " + reply), nil
				}
			}
		}
		return "acked", nil
	}

	if _, err := sendWTSMessageToActiveSessions(title, body, mbOK|mbIconInformation|mbSetForeground|mbTopmost, true); err != nil {
		return "", err
	}
	return "acked", nil
}

func showReplyDialogInUserSession(text string, allowReply bool) (string, error) {
	ids, err := interactiveSessionIDs()
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no active interactive session")
	}
	script := buildMessageDialogScript(text, allowReply)
	var lastErr error
	for _, sid := range ids {
		out, err := runPowerShellInSession(sid, script, dialogTimeout)
		if err != nil {
			lastErr = err
			continue
		}
		return out, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("user-session dialog failed")
}

func promptReplyInUserSession(hint string) (string, error) {
	if hint == "" {
		hint = "请输入要发给老师的简短回复（可留空）："
	}
	script := fmt.Sprintf(`
Add-Type -AssemblyName Microsoft.VisualBasic
$r = [Microsoft.VisualBasic.Interaction]::InputBox(%s, 'Proctor 教师消息', '')
$script:result = $r
[Console]::Out.WriteLine($r)
`, psSingleQuote(hint))
	ids, err := interactiveSessionIDs()
	if err != nil {
		return "", err
	}
	for _, sid := range ids {
		out, err := runPowerShellInSession(sid, script, dialogTimeout)
		if err == nil {
			return strings.TrimSpace(out), nil
		}
	}
	if currentProcessSessionID() != 0 {
		return runLocalPowerShellOutput(script)
	}
	return "", fmt.Errorf("input box unavailable")
}

func showReplyDialogPowerShell(text string, allowReply bool) (string, error) {
	return runLocalPowerShellOutput(buildMessageDialogScript(text, allowReply))
}

func buildMessageDialogScript(text string, allowReply bool) string {
	// Here-string avoids most escaping; neutralize a lone "'@" line terminator.
	safe := strings.ReplaceAll(text, "'@", "' @")
	allow := "$false"
	if allowReply {
		allow = "$true"
	}
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$msg = @'
%s
'@
$allowReply = %s
$form = New-Object System.Windows.Forms.Form
$form.Text = 'Proctor 教师消息'
$form.Width = 480
$form.Height = 300
$form.StartPosition = 'CenterScreen'
$form.TopMost = $true
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$form.ShowInTaskbar = $true
$label = New-Object System.Windows.Forms.Label
$label.Left = 16
$label.Top = 16
$label.Width = 430
$label.Height = 90
$label.Text = $msg
$form.Controls.Add($label)
$script:result = 'acked'
if ($allowReply) {
  $hint = New-Object System.Windows.Forms.Label
  $hint.Left = 16
  $hint.Top = 110
  $hint.Width = 430
  $hint.Height = 20
  $hint.Text = '可选：输入简短回复发给老师'
  $form.Controls.Add($hint)
  $box = New-Object System.Windows.Forms.TextBox
  $box.Left = 16
  $box.Top = 134
  $box.Width = 430
  $form.Controls.Add($box)
  $btnReply = New-Object System.Windows.Forms.Button
  $btnReply.Text = '发送回复'
  $btnReply.Width = 100
  $btnReply.Left = 240
  $btnReply.Top = 180
  $btnReply.Add_Click({
    $t = $box.Text.Trim()
    if ($t -ne '') { $script:result = 'reply: ' + $t } else { $script:result = 'acked' }
    $form.Close()
  })
  $form.Controls.Add($btnReply)
  $btnAck = New-Object System.Windows.Forms.Button
  $btnAck.Text = '知道了'
  $btnAck.Width = 100
  $btnAck.Left = 350
  $btnAck.Top = 180
  $btnAck.Add_Click({ $script:result = 'acked'; $form.Close() })
  $form.Controls.Add($btnAck)
  $form.AcceptButton = $btnReply
  $form.CancelButton = $btnAck
} else {
  $btnAck = New-Object System.Windows.Forms.Button
  $btnAck.Text = '知道了'
  $btnAck.Width = 100
  $btnAck.Left = 350
  $btnAck.Top = 180
  $btnAck.Add_Click({ $script:result = 'acked'; $form.Close() })
  $form.Controls.Add($btnAck)
  $form.AcceptButton = $btnAck
}
$form.Add_Shown({ $form.Activate() })
[void]$form.ShowDialog()
[Console]::Out.WriteLine($script:result)
`, safe, allow)
}

func runLocalPowerShellOutput(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", psEncodedCommand(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("powershell dialog: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func runPowerShellInSession(sessionID uint32, script string, timeout time.Duration) (string, error) {
	// Result file under Public avoids Session-0 pipe ACL/encoding issues.
	outPath, err := publicResultPath(sessionID)
	if err != nil {
		return "", err
	}
	defer os.Remove(outPath)

	wrapped := fmt.Sprintf(`
$__out = %s
$ErrorActionPreference = 'Stop'
$script:result = 'acked'
try {
%s
  Set-Content -LiteralPath $__out -Value $script:result -Encoding utf8
} catch {
  Set-Content -LiteralPath $__out -Value ("failed: " + $_.Exception.Message) -Encoding utf8
  exit 1
}
`, psSingleQuote(outPath), stripConsoleWriteLine(script))

	psPath := powershellPath()
	cmdline := fmt.Sprintf(`"%s" -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -EncodedCommand %s`,
		psPath, psEncodedCommand(wrapped))

	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return "", fmt.Errorf("WTSQueryUserToken session=%d: %w", sessionID, err)
	}
	defer userToken.Close()

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, userToken, false); err != nil {
		return "", fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return "", err
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		return "", err
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Desktop = desktop
	si.Flags = windows.STARTF_USESHOWWINDOW
	si.ShowWindow = windows.SW_HIDE

	var pi windows.ProcessInformation
	err = windows.CreateProcessAsUser(
		userToken,
		nil,
		cmdLinePtr,
		nil,
		nil,
		false,
		windows.CREATE_UNICODE_ENVIRONMENT,
		env,
		nil,
		&si,
		&pi,
	)
	if err != nil {
		return "", fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)

	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = uint32(dialogTimeout / time.Millisecond)
	}
	event, waitErr := windows.WaitForSingleObject(pi.Process, ms)
	if waitErr != nil {
		return "", fmt.Errorf("WaitForSingleObject: %w", waitErr)
	}
	if event == waitTimeout {
		_ = windows.TerminateProcess(pi.Process, 1)
		return "", fmt.Errorf("dialog timeout")
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		return "", fmt.Errorf("read dialog result: %w", err)
	}
	out := strings.TrimSpace(string(raw))
	// Strip UTF-8 BOM if present.
	out = strings.TrimPrefix(out, "\ufeff")
	if strings.HasPrefix(strings.ToLower(out), "failed:") {
		return "", fmt.Errorf("%s", out)
	}
	return out, nil
}

// stripConsoleWriteLine removes Console.Out writes; session runner persists $script:result instead.
func stripConsoleWriteLine(script string) string {
	lines := strings.Split(script, "\n")
	var kept []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[Console]::Out.WriteLine(") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func publicResultPath(sessionID uint32) (string, error) {
	public := strings.TrimSpace(os.Getenv("PUBLIC"))
	if public == "" {
		public = `C:\Users\Public`
	}
	dir := filepath.Join(public, "Proctor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("msg-%d-%d.txt", sessionID, time.Now().UnixNano())
	return filepath.Join(dir, name), nil
}

func powershellPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func psEncodedCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func sendWTSMessageToActiveSessions(title, text string, style uint32, wait bool) (uint32, error) {
	ids, err := interactiveSessionIDs()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("no active interactive session")
	}
	var lastErr error
	var lastResp uint32
	sent := 0
	for _, sid := range ids {
		resp, err := wtsSendMessage(sid, title, text, style, wait)
		if err != nil {
			lastErr = err
			continue
		}
		sent++
		lastResp = resp
		// One interactive dialog is enough.
		if wait {
			return resp, nil
		}
	}
	if sent == 0 {
		if lastErr != nil {
			return 0, lastErr
		}
		return 0, fmt.Errorf("WTSSendMessage failed for all sessions")
	}
	return lastResp, nil
}

func interactiveSessionIDs() ([]uint32, error) {
	seen := map[uint32]struct{}{}
	var ids []uint32
	add := func(sid uint32) {
		// Session 0 is the isolated services session — never target it.
		if sid == 0 || sid == 0xFFFFFFFF {
			return
		}
		if _, ok := seen[sid]; ok {
			return
		}
		seen[sid] = struct{}{}
		ids = append(ids, sid)
	}

	if console := windows.WTSGetActiveConsoleSessionId(); console != 0 && console != 0xFFFFFFFF {
		add(console)
	}

	var pInfo *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &pInfo, &count); err != nil {
		if len(ids) > 0 {
			return ids, nil
		}
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(pInfo)))

	sessions := unsafe.Slice(pInfo, count)
	for _, s := range sessions {
		if s.State == windows.WTSActive || s.State == windows.WTSConnected {
			add(s.SessionID)
		}
	}
	return ids, nil
}

func wtsSendMessage(sessionID uint32, title, text string, style uint32, wait bool) (uint32, error) {
	titleUTF16, err := windows.UTF16FromString(title)
	if err != nil {
		return 0, err
	}
	textUTF16, err := windows.UTF16FromString(text)
	if err != nil {
		return 0, err
	}
	var response uint32
	bWait := uintptr(0)
	timeout := uintptr(0)
	if wait {
		bWait = 1
		timeout = uintptr(wtsWaitSeconds)
	}
	r1, _, e1 := procWTSSendMessageW.Call(
		0, // WTS_CURRENT_SERVER_HANDLE
		uintptr(sessionID),
		uintptr(unsafe.Pointer(&titleUTF16[0])),
		uintptr(len(titleUTF16)*2),
		uintptr(unsafe.Pointer(&textUTF16[0])),
		uintptr(len(textUTF16)*2),
		uintptr(style),
		timeout,
		uintptr(unsafe.Pointer(&response)),
		bWait,
	)
	if r1 == 0 {
		if e1 != nil && e1 != syscall.Errno(0) {
			return 0, fmt.Errorf("WTSSendMessage session=%d: %w", sessionID, e1)
		}
		return 0, fmt.Errorf("WTSSendMessage session=%d failed", sessionID)
	}
	return response, nil
}

func currentProcessSessionID() uint32 {
	var sid uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sid); err != nil {
		return 0
	}
	return sid
}
