package agent

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/billcoding/proctor/internal/model"
)

func executeFSJob(job model.FSJob) model.FSJobResult {
	res := model.FSJobResult{
		JobID:   job.ID,
		AgentID: job.AgentID,
		Status:  "done",
	}
	out, err := runFSOp(job)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	res.Result = out
	return res
}

func runFSOp(job model.FSJob) (*model.FSResult, error) {
	switch job.Op {
	case "roots":
		return fsRoots()
	case "list":
		return fsList(job.Path)
	case "stat":
		return fsStat(job.Path)
	case "read":
		return fsRead(job.Path)
	case "write":
		return fsWrite(job.Path, job.Content)
	case "mkdir":
		return fsMkdir(job.Path)
	case "delete":
		return fsDelete(job.Path)
	case "rename":
		return fsRename(job.Path, job.Dest)
	default:
		return nil, fmt.Errorf("unsupported fs op: %s", job.Op)
	}
}

func cleanFSPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path traversal denied")
	}
	if !filepath.IsAbs(cleaned) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("relative path not allowed")
		}
		cleaned = filepath.Clean(filepath.Join(home, cleaned))
	}
	return cleaned, nil
}

func fsRoots() (*model.FSResult, error) {
	var entries []model.FSEntry
	now := time.Now().UTC()
	home, _ := os.UserHomeDir()
	if home != "" {
		entries = append(entries, model.FSEntry{
			Name: "Home", Path: home, IsDir: true, ModTime: now, Mode: "dir",
		})
	}
	if runtime.GOOS == "windows" {
		for _, letter := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
			root := string(letter) + `:\`
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				entries = append(entries, model.FSEntry{
					Name: root, Path: root, IsDir: true, ModTime: st.ModTime().UTC(), Mode: "dir",
				})
			}
		}
	} else {
		if st, err := os.Stat("/"); err == nil {
			entries = append(entries, model.FSEntry{
				Name: "/", Path: "/", IsDir: true, ModTime: st.ModTime().UTC(), Mode: "dir",
			})
		}
		if runtime.GOOS == "darwin" {
			if st, err := os.Stat("/Users"); err == nil && st.IsDir() {
				entries = append(entries, model.FSEntry{
					Name: "Users", Path: "/Users", IsDir: true, ModTime: st.ModTime().UTC(), Mode: "dir",
				})
			}
		}
	}
	desktop := filepath.Join(home, "Desktop")
	if st, err := os.Stat(desktop); err == nil && st.IsDir() {
		entries = append(entries, model.FSEntry{
			Name: "Desktop", Path: desktop, IsDir: true, ModTime: st.ModTime().UTC(), Mode: "dir",
		})
	}
	docs := filepath.Join(home, "Documents")
	if st, err := os.Stat(docs); err == nil && st.IsDir() {
		entries = append(entries, model.FSEntry{
			Name: "Documents", Path: docs, IsDir: true, ModTime: st.ModTime().UTC(), Mode: "dir",
		})
	}
	return &model.FSResult{Op: "roots", Entries: entries, Message: "ok"}, nil
}

func fsList(path string) (*model.FSResult, error) {
	if strings.TrimSpace(path) == "" {
		return fsRoots()
	}
	dir, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]model.FSEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, e.Name())
		out = append(out, model.FSEntry{
			Name:    e.Name(),
			Path:    full,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return &model.FSResult{Op: "list", Path: dir, Entries: out, Message: "ok"}, nil
}

func fsStat(path string) (*model.FSResult, error) {
	p, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	return &model.FSResult{
		Op: "stat", Path: p, Name: st.Name(), IsDir: st.IsDir(),
		Size: st.Size(), Mode: st.Mode().String(), ModTime: st.ModTime().UTC(), Message: "ok",
	}, nil
}

func fsRead(path string) (*model.FSResult, error) {
	p, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limit := int64(model.MaxFSFileBytes)
	truncated := false
	if st.Size() > limit {
		truncated = true
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	return &model.FSResult{
		Op: "read", Path: p, Name: st.Name(), Size: st.Size(),
		Mode: st.Mode().String(), ModTime: st.ModTime().UTC(),
		Content: base64.StdEncoding.EncodeToString(data), Truncated: truncated, Message: "ok",
	}, nil
}

func fsWrite(path, contentB64 string) (*model.FSResult, error) {
	p, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 content")
	}
	if len(raw) > model.MaxFSFileBytes {
		return nil, fmt.Errorf("file too large (max %d bytes)", model.MaxFSFileBytes)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		return nil, err
	}
	st, _ := os.Stat(p)
	res := &model.FSResult{Op: "write", Path: p, Message: "written", Size: int64(len(raw))}
	if st != nil {
		res.Name = st.Name()
		res.ModTime = st.ModTime().UTC()
		res.Mode = st.Mode().String()
	}
	return res, nil
}

func fsMkdir(path string) (*model.FSResult, error) {
	p, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, err
	}
	return &model.FSResult{Op: "mkdir", Path: p, IsDir: true, Message: "created"}, nil
}

func fsDelete(path string) (*model.FSResult, error) {
	p, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("directory not empty")
		}
	}
	if err := os.Remove(p); err != nil {
		return nil, err
	}
	return &model.FSResult{Op: "delete", Path: p, Message: "deleted"}, nil
}

func fsRename(path, dest string) (*model.FSResult, error) {
	src, err := cleanFSPath(path)
	if err != nil {
		return nil, err
	}
	dst, err := cleanFSPath(dest)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	return &model.FSResult{Op: "rename", Path: dst, Message: "renamed"}, nil
}
