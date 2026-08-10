package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/model"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// ShellHub bridges teacher browsers to SSH sessions or agent reverse shells.
type ShellHub struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

type shellSession struct {
	ID      string
	AgentID string
	Mode    string // ssh | agent

	teacher   *websocket.Conn
	agent     *websocket.Conn
	sshClient *ssh.Client
	sshSess   *ssh.Session
	sshStdin  io.WriteCloser

	cols, rows int
	pending    bool
	createdAt  time.Time

	writeMu sync.Mutex
}

func NewShellHub() *ShellHub {
	h := &ShellHub{sessions: map[string]*shellSession{}}
	go h.reaper()
	return h
}

func (h *ShellHub) reaper() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		h.mu.Lock()
		now := time.Now()
		for id, s := range h.sessions {
			if now.Sub(s.createdAt) > 2*time.Hour {
				h.closeLocked(id)
			}
		}
		h.mu.Unlock()
	}
}

func (h *ShellHub) PendingForAgent(agentID string) []model.ShellOffer {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []model.ShellOffer
	for _, s := range h.sessions {
		if s.AgentID == agentID && s.Mode == "agent" && s.pending && s.agent == nil {
			out = append(out, model.ShellOffer{SessionID: s.ID, Cols: s.cols, Rows: s.rows})
		}
	}
	return out
}

func (h *ShellHub) closeLocked(id string) {
	s, ok := h.sessions[id]
	if !ok {
		return
	}
	delete(h.sessions, id)
	if s.teacher != nil {
		_ = s.teacher.Close()
	}
	if s.agent != nil {
		_ = s.agent.Close()
	}
	if s.sshSess != nil {
		_ = s.sshSess.Close()
	}
	if s.sshClient != nil {
		_ = s.sshClient.Close()
	}
}

func (h *ShellHub) Close(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked(id)
}

func (a *API) handleTeacherShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[1] != "shell" {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
		return
	}
	agentID := parts[0]

	tok := r.Header.Get("X-Admin-Token")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	if a.cfg.AdminToken != "" && tok != a.cfg.AdminToken {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
		return
	}

	info, _, err := a.store.GetAgent(agentID, a.online)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "agent not found"})
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// First message configures the session.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}
	var cfg struct {
		Mode     string `json:"mode"` // ssh | agent
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Cols     int    `json:"cols"`
		Rows     int    `json:"rows"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		_ = conn.WriteJSON(model.ShellWSMessage{Type: "error", Message: "invalid start config"})
		_ = conn.Close()
		return
	}
	if cfg.Mode == "" {
		cfg.Mode = "agent"
	}
	if cfg.Cols <= 0 {
		cfg.Cols = 120
	}
	if cfg.Rows <= 0 {
		cfg.Rows = 36
	}
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	if cfg.Host == "" {
		cfg.Host = info.IP
	}

	sess := &shellSession{
		ID: common.NewID("sh"), AgentID: agentID, Mode: cfg.Mode,
		teacher: conn, cols: cfg.Cols, rows: cfg.Rows, createdAt: time.Now(),
	}

	a.shell.mu.Lock()
	a.shell.sessions[sess.ID] = sess
	a.shell.mu.Unlock()

	switch cfg.Mode {
	case "ssh":
		if err := a.startSSHSession(sess, cfg.Host, cfg.Port, cfg.User, cfg.Password); err != nil {
			_ = conn.WriteJSON(model.ShellWSMessage{Type: "error", Message: err.Error()})
			a.shell.Close(sess.ID)
			return
		}
		_ = conn.WriteJSON(model.ShellWSMessage{Type: "ready", Message: fmt.Sprintf("SSH %s@%s:%d", cfg.User, cfg.Host, cfg.Port)})
		a.pumpTeacherToSSH(sess)
	case "agent":
		sess.pending = true
		_ = conn.WriteJSON(model.ShellWSMessage{Type: "ready", Message: "等待学生机 Agent 接入终端（下一次心跳，约数秒）…"})
		a.pumpTeacherToAgent(sess)
	default:
		_ = conn.WriteJSON(model.ShellWSMessage{Type: "error", Message: "unsupported mode"})
		a.shell.Close(sess.ID)
	}
}

func (a *API) handleAgentShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	if a.cfg.AgentToken != "" {
		tok := r.Header.Get("X-Agent-Token")
		if tok == "" {
			tok = r.URL.Query().Get("agent_token")
		}
		if tok != a.cfg.AgentToken {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized agent"})
			return
		}
	}
	agentID := r.URL.Query().Get("agent_id")
	sessionID := r.URL.Query().Get("session_id")
	if agentID == "" || sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_id and session_id required"})
		return
	}

	a.shell.mu.Lock()
	sess := a.shell.sessions[sessionID]
	if sess == nil || sess.AgentID != agentID || sess.Mode != "agent" {
		a.shell.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "session not found"})
		return
	}
	if sess.agent != nil {
		a.shell.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "session already connected"})
		return
	}
	a.shell.mu.Unlock()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	a.shell.mu.Lock()
	sess = a.shell.sessions[sessionID]
	if sess == nil {
		a.shell.mu.Unlock()
		_ = conn.Close()
		return
	}
	sess.agent = conn
	sess.pending = false
	teacher := sess.teacher
	cols, rows := sess.cols, sess.rows
	a.shell.mu.Unlock()

	_ = writeWS(conn, model.ShellWSMessage{Type: "ready", Cols: cols, Rows: rows})
	if teacher != nil {
		_ = writeWS(teacher, model.ShellWSMessage{Type: "ready", Message: "Agent 终端已连接"})
	}

	// Agent -> Teacher
	go func() {
		defer a.shell.Close(sessionID)
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
			case "output", "error":
				a.shell.mu.Lock()
				s := a.shell.sessions[sessionID]
				var t *websocket.Conn
				if s != nil {
					t = s.teacher
				}
				a.shell.mu.Unlock()
				if t != nil {
					_ = writeWS(t, msg)
				}
			case "ping":
				_ = writeWS(conn, model.ShellWSMessage{Type: "pong"})
			}
		}
	}()
}

func (a *API) startSSHSession(sess *shellSession, host string, port int, user, password string) error {
	if user == "" {
		return fmt.Errorf("SSH 用户名不能为空")
	}
	if host == "" {
		return fmt.Errorf("SSH 主机不能为空")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // classroom LAN convenience
		Timeout:         12 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("SSH 连接失败: %w", err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", sess.rows, sess.cols, modes); err != nil {
		_ = session.Close()
		_ = client.Close()
		return fmt.Errorf("请求 PTY 失败: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return err
	}
	if err := session.Shell(); err != nil {
		_ = session.Close()
		_ = client.Close()
		return fmt.Errorf("启动 shell 失败: %w", err)
	}
	sess.sshClient = client
	sess.sshSess = session
	sess.sshStdin = stdin

	copyOut := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_ = sess.writeTeacher(model.ShellWSMessage{Type: "output", Data: string(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}
	go copyOut(stdout)
	go copyOut(stderr)
	go func() {
		_ = session.Wait()
		_ = sess.writeTeacher(model.ShellWSMessage{Type: "error", Message: "SSH 会话已结束"})
		a.shell.Close(sess.ID)
	}()
	return nil
}

func (a *API) pumpTeacherToSSH(sess *shellSession) {
	defer a.shell.Close(sess.ID)
	for {
		_, raw, err := sess.teacher.ReadMessage()
		if err != nil {
			return
		}
		var msg model.ShellWSMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "input":
			if sess.sshStdin != nil && msg.Data != "" {
				if _, err := sess.sshStdin.Write([]byte(msg.Data)); err != nil {
					return
				}
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 && sess.sshSess != nil {
				sess.cols, sess.rows = msg.Cols, msg.Rows
				_ = sess.sshSess.WindowChange(msg.Rows, msg.Cols)
			}
		case "ping":
			_ = writeWS(sess.teacher, model.ShellWSMessage{Type: "pong"})
		}
	}
}

func (a *API) pumpTeacherToAgent(sess *shellSession) {
	defer a.shell.Close(sess.ID)
	for {
		_, raw, err := sess.teacher.ReadMessage()
		if err != nil {
			return
		}
		var msg model.ShellWSMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		a.shell.mu.Lock()
		s := a.shell.sessions[sess.ID]
		var agent *websocket.Conn
		if s != nil {
			agent = s.agent
			if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				s.cols, s.rows = msg.Cols, msg.Rows
			}
		}
		a.shell.mu.Unlock()
		if agent == nil {
			continue
		}
		switch msg.Type {
		case "input", "resize", "ping":
			if err := writeWS(agent, msg); err != nil {
				return
			}
		}
	}
}

func writeWS(conn *websocket.Conn, msg model.ShellWSMessage) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	return conn.WriteJSON(msg)
}

func (s *shellSession) writeTeacher(msg model.ShellWSMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.teacher == nil {
		return fmt.Errorf("no teacher")
	}
	return s.teacher.WriteJSON(msg)
}