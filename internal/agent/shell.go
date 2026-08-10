package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/model"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var activeShells sync.Map

func (r *Runtime) handleShellOffers(offers []model.ShellOffer) {
	for _, offer := range offers {
		if _, loaded := activeShells.LoadOrStore(offer.SessionID, true); loaded {
			continue
		}
		go func(o model.ShellOffer) {
			defer activeShells.Delete(o.SessionID)
			r.connectShellSession(o)
		}(offer)
	}
}

func (r *Runtime) connectShellSession(offer model.ShellOffer) {
	u, err := url.Parse(r.cfg.ServerURL)
	if err != nil {
		log.Printf("shell: bad server url: %v", err)
		return
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/api/agent/shell"
	q := u.Query()
	q.Set("agent_id", r.cfg.AgentID)
	q.Set("session_id", offer.SessionID)
	if r.cfg.AgentToken != "" {
		q.Set("agent_token", r.cfg.AgentToken)
	}
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	header := http.Header{}
	if r.cfg.AgentToken != "" {
		header.Set("X-Agent-Token", r.cfg.AgentToken)
	}
	conn, _, err := dialer.Dial(u.String(), header)
	if err != nil {
		log.Printf("shell: dial failed: %v", err)
		return
	}
	defer conn.Close()

	cols, rows := offer.Cols, offer.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 36
	}

	// Wait for ready from server (optional).
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	_, _, _ = conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})

	cmd := shellCommand()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		_ = conn.WriteJSON(model.ShellWSMessage{Type: "error", Message: "start pty: " + err.Error()})
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
	}()

	var writeMu sync.Mutex
	writeJSON := func(msg model.ShellWSMessage) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteJSON(msg)
	}
	writeJSON(model.ShellWSMessage{Type: "output", Data: fmt.Sprintf("[proctor] local shell started (%s)\r\n", runtime.GOOS)})

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				writeJSON(model.ShellWSMessage{Type: "output", Data: string(buf[:n])})
			}
			if err != nil {
				if err != io.EOF {
					writeJSON(model.ShellWSMessage{Type: "error", Message: err.Error()})
				}
				_ = conn.Close()
				return
			}
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg model.ShellWSMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "input":
			if msg.Data != "" {
				if _, err := ptmx.Write([]byte(msg.Data)); err != nil {
					return
				}
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)})
			}
		case "ping":
			writeJSON(model.ShellWSMessage{Type: "pong"})
		}
	}
}

func shellCommand() *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("powershell.exe", "-NoLogo")
		cmd.Env = os.Environ()
		return cmd
	default:
		shell := os.Getenv("SHELL")
		if shell == "" {
			if _, err := os.Stat("/bin/bash"); err == nil {
				shell = "/bin/bash"
			} else {
				shell = "/bin/sh"
			}
		}
		cmd := exec.Command(shell)
		if strings.Contains(shell, "bash") {
			cmd = exec.Command(shell, "-l")
		}
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		return cmd
	}
}
