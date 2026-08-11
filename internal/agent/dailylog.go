package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultAgentLogName = "agent.log"

// resolveDefaultLogPath returns <cwd>/logs/agent.log.
// Prefer process cwd; if cwd is missing/unstable (common for Windows services
// under System32) or logs/ cannot be created there, fall back to
// <exeDir>/logs/agent.log.
func resolveDefaultLogPath() string {
	return filepath.Join(resolveLogDir(), defaultAgentLogName)
}

func resolveLogDir() string {
	if cwd, err := os.Getwd(); err == nil && cwd != "" && !isUnstableCWD(cwd) {
		dir := filepath.Join(cwd, "logs")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err2 := filepath.EvalSymlinks(exe); err2 == nil {
			exe = resolved
		}
		dir := filepath.Join(filepath.Dir(exe), "logs")
		_ = os.MkdirAll(dir, 0o755)
		return dir
	}
	dir := "logs"
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// isUnstableCWD detects service/system working directories that are poor
// locations for writing logs (e.g. /, C:\, C:\Windows\System32).
func isUnstableCWD(cwd string) bool {
	clean := filepath.Clean(cwd)
	if clean == string(filepath.Separator) {
		return true
	}
	vol := filepath.VolumeName(clean)
	if vol != "" {
		rest := strings.TrimPrefix(clean, vol)
		if rest == `\` || rest == `/` || rest == "" {
			return true
		}
	}
	lower := strings.ToLower(clean)
	if strings.HasSuffix(lower, `\windows\system32`) ||
		strings.HasSuffix(lower, `/windows/system32`) ||
		strings.HasSuffix(lower, `\windows`) ||
		strings.HasSuffix(lower, `/windows`) {
		return true
	}
	return false
}

// dailyLogWriter writes to path and rotates once per local calendar day.
// Active file stays at path (e.g. logs/agent.log); previous days become
// <base>.YYYY-MM-DD<ext> (e.g. logs/agent.2026-08-10.log).
type dailyLogWriter struct {
	path string

	mu   sync.Mutex
	file *os.File
	day  string // local YYYY-MM-DD of the open file
}

func openDailyLog(path string) (*dailyLogWriter, error) {
	w := &dailyLogWriter{path: path}
	if err := w.rotateIfNeeded(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeeded(time.Now()); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// rotateIfNeeded must be called with w.mu held (except from openDailyLog before publish).
func (w *dailyLogWriter) rotateIfNeeded(now time.Time) error {
	today := now.Format("2006-01-02")
	if w.file != nil && w.day == today {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}

	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
		if err := archiveLogFile(w.path, w.day); err != nil {
			return err
		}
	} else if st, err := os.Stat(w.path); err == nil && !st.IsDir() {
		// Startup: existing agent.log from a previous local day → archive first.
		fileDay := st.ModTime().In(now.Location()).Format("2006-01-02")
		if fileDay != today {
			if err := archiveLogFile(w.path, fileDay); err != nil {
				return err
			}
		}
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.day = today
	return nil
}

func archiveLogFile(path, day string) error {
	if day == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dest := archivedLogPath(path, day)
	if _, err := os.Stat(dest); err == nil {
		// Collision (e.g. multiple restarts same archive day): pick a free name.
		for i := 1; i < 1000; i++ {
			alt := archivedLogPath(path, fmt.Sprintf("%s.%d", day, i))
			if _, err := os.Stat(alt); os.IsNotExist(err) {
				dest = alt
				break
			}
		}
	}
	return os.Rename(path, dest)
}

func archivedLogPath(path, day string) string {
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	if ext == "" {
		return path + "." + day
	}
	return base + "." + day + ext
}

// Ensure dailyLogWriter implements io.WriteCloser.
var _ io.WriteCloser = (*dailyLogWriter)(nil)
