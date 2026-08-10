package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const autoUpdateMinInterval = 5 * time.Minute

// UpdateInfo is returned by the server check endpoint.
type UpdateInfo struct {
	OK          bool   `json:"ok"`
	Update      bool   `json:"update"`
	Latest      string `json:"latest"`
	Target      string `json:"target,omitempty"`
	Current     string `json:"current"`
	Force       bool   `json:"force"`
	Notes       string `json:"notes,omitempty"`
	DownloadURL string `json:"download_url"`
	File        string `json:"file,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Updater performs OTA self-update of the running agent binary.
type Updater struct {
	client     *Client
	configPath string

	mu        sync.Mutex
	busy      bool
	lastCheck time.Time
}

func NewUpdater(client *Client, configPath string) *Updater {
	return &Updater{client: client, configPath: configPath}
}

// CheckAndApply queries the server for latest and applies when newer.
// If force is false, respects the minimum check interval.
// Returns (applied, error). applied=true means binary was replaced and restart was triggered.
func (u *Updater) CheckAndApply(force bool) (bool, error) {
	return u.apply("", force, false)
}

// ApplyVersion downloads and installs a specific published version (teacher-directed).
// Allows downgrade / pin; skips only when already on that version.
func (u *Updater) ApplyVersion(target string) (bool, error) {
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(target, "v"), "V"))
	if target == "" {
		return u.CheckAndApply(true)
	}
	return u.apply(target, true, true)
}

func (u *Updater) apply(target string, force bool, pinTarget bool) (bool, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return false, fmt.Errorf("update already in progress")
	}
	if !force && !u.lastCheck.IsZero() && time.Since(u.lastCheck) < autoUpdateMinInterval {
		u.mu.Unlock()
		return false, nil
	}
	u.busy = true
	u.lastCheck = time.Now()
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()

	info, err := u.client.CheckUpdate(runtime.GOOS, runtime.GOARCH, Version, target)
	if err != nil {
		return false, fmt.Errorf("check update: %w", err)
	}
	if !info.OK {
		return false, fmt.Errorf("check update rejected: %s", info.Error)
	}
	want := strings.TrimSpace(info.Target)
	if want == "" {
		want = strings.TrimSpace(info.Latest)
	}
	if want == "" {
		return false, nil
	}

	var need bool
	if pinTarget {
		need = info.Update || !versionEqual(Version, want)
	} else {
		need = info.Update || info.Force || versionLess(Version, want)
		if versionEqual(Version, want) {
			need = false
		}
	}
	if !need {
		if force {
			log.Printf("update: already at version %s", Version)
		}
		return false, nil
	}
	if info.DownloadURL == "" {
		return false, fmt.Errorf("update available (%s) but download_url empty", want)
	}

	log.Printf("update: applying %s → %s (%s)", Version, want, info.Notes)
	tmp, err := u.client.DownloadUpdate(info.DownloadURL)
	if err != nil {
		return false, fmt.Errorf("download: %w", err)
	}
	defer os.Remove(tmp)

	if err := verifyDownload(tmp, info.Size, info.SHA256); err != nil {
		return false, fmt.Errorf("verify: %w", err)
	}

	exe, err := currentExecutable()
	if err != nil {
		return false, err
	}
	if err := replaceExecutable(exe, tmp); err != nil {
		return false, fmt.Errorf("replace binary: %w", err)
	}
	log.Printf("update: binary replaced at %s (backup .bak if supported)", exe)

	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := restartAfterUpdate(exe, u.configPath); err != nil {
			log.Printf("update: restart failed: %v (please restart proctor-agent manually)", err)
			return
		}
	}()
	return true, nil
}

func verifyDownload(path string, expectSize int64, expectSHA string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		return fmt.Errorf("downloaded file is empty")
	}
	if expectSize > 0 && st.Size() != expectSize {
		return fmt.Errorf("size mismatch: got %d want %d", st.Size(), expectSize)
	}
	if expectSHA == "" {
		return nil
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !equalFoldHex(sum, expectSHA) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", sum, expectSHA)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func equalFoldHex(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'F' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'F' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// copyFile writes src to dst (creating/truncating), preserving mode when possible.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
