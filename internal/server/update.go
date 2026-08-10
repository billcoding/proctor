package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/model"
)

// Multi-version OTA layout under data/updates/:
//
//	index.json                 # latest + versions[]
//	0.1.0/version.json         # per-version manifest + binaries
//	0.1.0/proctor-agent-...
//
// New publishes always write updates/<version>/ (no leading "v").
// Legacy flat layout (root version.json + binaries) is still readable.
// Old v<version>/ dirs are also recognized when reading.

// Manifest describes one published agent version.
type Manifest struct {
	Version   string                      `json:"version"`
	Force     bool                        `json:"force"`
	Notes     string                      `json:"notes,omitempty"`
	CreatedAt string                      `json:"created_at,omitempty"`
	Artifacts map[string]ManifestArtifact `json:"artifacts,omitempty"`
}

type ManifestArtifact struct {
	File   string `json:"file,omitempty"` // optional override; default proctor-agent-{os}-{arch}
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// UpdateIndex is the multi-version catalog at updates/index.json.
type UpdateIndex struct {
	Latest   string             `json:"latest"`
	Versions []UpdateIndexEntry `json:"versions"`
}

type UpdateIndexEntry struct {
	Version   string `json:"version"`
	Notes     string `json:"notes,omitempty"`
	Force     bool   `json:"force,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// VersionInfo is the admin/API view of one published version.
type VersionInfo struct {
	Version   string   `json:"version"`
	Notes     string   `json:"notes,omitempty"`
	Force     bool     `json:"force"`
	CreatedAt string   `json:"created_at,omitempty"`
	Platforms []string `json:"platforms"`
	IsLatest  bool     `json:"is_latest"`
	Legacy    bool     `json:"legacy,omitempty"`
}

func (a *API) updatesDir() string {
	return filepath.Join(a.cfg.DataDir, "updates")
}

func versionDirName(version string) string {
	return normalizeVer(version)
}

func (a *API) versionDir(version string) string {
	return filepath.Join(a.updatesDir(), versionDirName(version))
}

// versionDirs returns candidate on-disk directories for a version (canonical first).
func (a *API) versionDirs(version string) []string {
	v := normalizeVer(version)
	if v == "" {
		return nil
	}
	root := a.updatesDir()
	return []string{
		filepath.Join(root, v),      // canonical: updates/0.2.0/
		filepath.Join(root, "v"+v), // legacy read: updates/v0.2.0/
	}
}

func looksLikeVersionDir(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	v := normalizeVer(name)
	if v == "" {
		return false
	}
	// Accept semver-ish or ISO build stamps used by scripts.
	if _, ok := parseSemver(v); ok {
		return true
	}
	return strings.ContainsAny(v, ".-T:")
}

func (a *API) indexPath() string {
	return filepath.Join(a.updatesDir(), "index.json")
}

func (a *API) flatManifestPath() string {
	return filepath.Join(a.updatesDir(), "version.json")
}

func platformsFromManifest(m Manifest) []string {
	keys := make([]string, 0, len(m.Artifacts))
	for k := range m.Artifacts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readManifestFile(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	if m.Version == "" {
		return Manifest{}, fmt.Errorf("%s: empty version", filepath.Base(path))
	}
	m.Version = normalizeVer(m.Version)
	return m, nil
}

func (a *API) loadFlatManifest() (Manifest, error) {
	return readManifestFile(a.flatManifestPath())
}

func (a *API) loadVersionManifest(version string) (Manifest, string, error) {
	v := normalizeVer(version)
	if v == "" {
		return Manifest{}, "", fmt.Errorf("empty version")
	}
	var lastErr error
	for _, dir := range a.versionDirs(v) {
		m, err := readManifestFile(filepath.Join(dir, "version.json"))
		if err == nil {
			if normalizeVer(m.Version) == "" {
				m.Version = v
			}
			return m, dir, nil
		}
		lastErr = err
		if !os.IsNotExist(err) {
			return Manifest{}, "", err
		}
	}
	// Legacy flat layout: only when requested version matches root manifest.
	flat, ferr := a.loadFlatManifest()
	if ferr != nil {
		if os.IsNotExist(ferr) {
			if lastErr != nil {
				return Manifest{}, "", fmt.Errorf("version not found: %s", v)
			}
			return Manifest{}, "", fmt.Errorf("version not found: %s", v)
		}
		return Manifest{}, "", ferr
	}
	if !versionEqual(flat.Version, v) {
		return Manifest{}, "", fmt.Errorf("version not found: %s", v)
	}
	return flat, a.updatesDir(), nil
}

func (a *API) scanVersionDirs() ([]UpdateIndexEntry, error) {
	dir := a.updatesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var out []UpdateIndexEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !looksLikeVersionDir(name) {
			continue
		}
		m, err := readManifestFile(filepath.Join(dir, name, "version.json"))
		if err != nil {
			continue
		}
		ver := normalizeVer(m.Version)
		if ver == "" {
			ver = normalizeVer(name)
		}
		if seen[ver] {
			continue
		}
		seen[ver] = true
		created := m.CreatedAt
		if created == "" {
			if st, err := e.Info(); err == nil {
				created = st.ModTime().UTC().Format(time.RFC3339)
			}
		}
		out = append(out, UpdateIndexEntry{
			Version: ver, Notes: m.Notes, Force: m.Force, CreatedAt: created,
		})
	}
	return out, nil
}

func sortIndexEntries(entries []UpdateIndexEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		// Newer first when semver-comparable; else by created_at, then version string.
		if versionLess(entries[j].Version, entries[i].Version) {
			return true
		}
		if versionLess(entries[i].Version, entries[j].Version) {
			return false
		}
		return entries[i].CreatedAt > entries[j].CreatedAt
	})
}

func (a *API) loadIndex() (UpdateIndex, error) {
	path := a.indexPath()
	b, err := os.ReadFile(path)
	if err == nil {
		var idx UpdateIndex
		if err := json.Unmarshal(b, &idx); err != nil {
			return UpdateIndex{}, err
		}
		idx.Latest = normalizeVer(idx.Latest)
		for i := range idx.Versions {
			idx.Versions[i].Version = normalizeVer(idx.Versions[i].Version)
		}
		// Merge any on-disk version dirs not yet listed (manual drops).
		seen := map[string]bool{}
		for _, e := range idx.Versions {
			seen[e.Version] = true
		}
		scanned, _ := a.scanVersionDirs()
		for _, e := range scanned {
			if !seen[e.Version] {
				idx.Versions = append(idx.Versions, e)
				seen[e.Version] = true
			}
		}
		// Include legacy flat if present and not listed.
		if flat, err := a.loadFlatManifest(); err == nil {
			v := normalizeVer(flat.Version)
			if v != "" && !seen[v] {
				created := flat.CreatedAt
				if created == "" {
					if st, err := os.Stat(a.flatManifestPath()); err == nil {
						created = st.ModTime().UTC().Format(time.RFC3339)
					}
				}
				idx.Versions = append(idx.Versions, UpdateIndexEntry{
					Version: v, Notes: flat.Notes, Force: flat.Force, CreatedAt: created,
				})
			}
		}
		if idx.Latest == "" && len(idx.Versions) > 0 {
			sortIndexEntries(idx.Versions)
			idx.Latest = idx.Versions[0].Version
		}
		sortIndexEntries(idx.Versions)
		return idx, nil
	}
	if !os.IsNotExist(err) {
		return UpdateIndex{}, err
	}

	// No index.json: synthesize from version dirs and/or flat layout.
	idx := UpdateIndex{}
	scanned, err := a.scanVersionDirs()
	if err != nil {
		return UpdateIndex{}, err
	}
	idx.Versions = append(idx.Versions, scanned...)
	if flat, err := a.loadFlatManifest(); err == nil {
		v := normalizeVer(flat.Version)
		dup := false
		for _, e := range idx.Versions {
			if versionEqual(e.Version, v) {
				dup = true
				break
			}
		}
		if !dup && v != "" {
			created := flat.CreatedAt
			if created == "" {
				if st, err := os.Stat(a.flatManifestPath()); err == nil {
					created = st.ModTime().UTC().Format(time.RFC3339)
				}
			}
			idx.Versions = append(idx.Versions, UpdateIndexEntry{
				Version: v, Notes: flat.Notes, Force: flat.Force, CreatedAt: created,
			})
		}
	}
	if len(idx.Versions) == 0 {
		return UpdateIndex{}, os.ErrNotExist
	}
	sortIndexEntries(idx.Versions)
	idx.Latest = idx.Versions[0].Version
	return idx, nil
}

func (a *API) saveIndex(idx UpdateIndex) error {
	idx.Latest = normalizeVer(idx.Latest)
	for i := range idx.Versions {
		idx.Versions[i].Version = normalizeVer(idx.Versions[i].Version)
	}
	sortIndexEntries(idx.Versions)
	if err := os.MkdirAll(a.updatesDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := a.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.indexPath())
}

func (a *API) listVersionInfos() ([]VersionInfo, error) {
	idx, err := a.loadIndex()
	if err != nil {
		if os.IsNotExist(err) {
			return []VersionInfo{}, nil
		}
		return nil, err
	}
	out := make([]VersionInfo, 0, len(idx.Versions))
	for _, e := range idx.Versions {
		info := VersionInfo{
			Version:   e.Version,
			Notes:     e.Notes,
			Force:     e.Force,
			CreatedAt: e.CreatedAt,
			IsLatest:  versionEqual(e.Version, idx.Latest),
			Platforms: []string{},
		}
		m, dir, err := a.loadVersionManifest(e.Version)
		if err == nil {
			if info.Notes == "" {
				info.Notes = m.Notes
			}
			info.Force = m.Force
			if info.CreatedAt == "" {
				info.CreatedAt = m.CreatedAt
			}
			info.Platforms = platformsFromManifest(m)
			if dir == a.updatesDir() {
				info.Legacy = true
			}
			// Prefer platforms that actually exist on disk.
			if len(info.Platforms) == 0 {
				info.Platforms = a.discoverPlatforms(dir)
			}
		} else {
			info.Platforms = a.discoverPlatforms(a.versionDir(e.Version))
			if len(info.Platforms) == 0 {
				info.Platforms = a.discoverPlatforms(a.updatesDir())
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (a *API) discoverPlatforms(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var plats []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "proctor-agent-") {
			continue
		}
		rest := strings.TrimPrefix(name, "proctor-agent-")
		rest = strings.TrimSuffix(rest, ".exe")
		if rest == "" || !strings.Contains(rest, "-") {
			continue
		}
		plats = append(plats, rest)
	}
	sort.Strings(plats)
	return plats
}

func (a *API) getVersionInfo(version string) (VersionInfo, error) {
	v := normalizeVer(version)
	list, err := a.listVersionInfos()
	if err != nil {
		return VersionInfo{}, err
	}
	for _, info := range list {
		if versionEqual(info.Version, v) {
			return info, nil
		}
	}
	return VersionInfo{}, fmt.Errorf("version not found: %s", v)
}

func artifactKey(goos, goarch string) string {
	return strings.ToLower(goos) + "-" + strings.ToLower(goarch)
}

func defaultArtifactName(goos, goarch string) string {
	name := "proctor-agent-" + artifactKey(goos, goarch)
	if strings.EqualFold(goos, "windows") {
		name += ".exe"
	}
	return name
}

func (a *API) resolveArtifactInDir(dir string, m Manifest, goos, goarch string) (fileName string, art ManifestArtifact, err error) {
	key := artifactKey(goos, goarch)
	art, ok := m.Artifacts[key]
	if !ok {
		art = ManifestArtifact{}
	}
	fileName = strings.TrimSpace(art.File)
	if fileName == "" {
		fileName = defaultArtifactName(goos, goarch)
	}
	fileName = filepath.Base(fileName)
	full := filepath.Join(dir, fileName)
	st, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", art, fmt.Errorf("artifact not found: %s", fileName)
		}
		return "", art, err
	}
	if st.IsDir() {
		return "", art, fmt.Errorf("artifact is a directory: %s", fileName)
	}
	if art.Size <= 0 {
		art.Size = st.Size()
	}
	return fileName, art, nil
}

func (a *API) resolveArtifact(version, goos, goarch string) (m Manifest, dir, fileName string, art ManifestArtifact, err error) {
	m, dir, err = a.loadVersionManifest(version)
	if err != nil {
		return Manifest{}, "", "", ManifestArtifact{}, err
	}
	fileName, art, err = a.resolveArtifactInDir(dir, m, goos, goarch)
	if err != nil {
		return Manifest{}, "", "", ManifestArtifact{}, err
	}
	return m, dir, fileName, art, nil
}

func (a *API) latestVersion() (string, error) {
	idx, err := a.loadIndex()
	if err != nil {
		return "", err
	}
	return normalizeVer(idx.Latest), nil
}

// handleUpdates lists published versions or creates nothing (GET only at collection).
func (a *API) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	list, err := a.listVersionInfos()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	latest := ""
	for _, v := range list {
		if v.IsLatest {
			latest = v.Version
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latest": latest, "versions": list})
}

func (a *API) handleUpdatesSub(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/updates/"), "/")
	if path == "" {
		a.handleUpdates(w, r)
		return
	}
	if path == "latest" {
		a.handleUpdatesLatest(w, r)
		return
	}
	version := path
	switch r.Method {
	case http.MethodGet:
		info, err := a.getVersionInfo(version)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		m, _, err := a.loadVersionManifest(version)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": info})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": info, "manifest": m})
	case http.MethodDelete:
		if err := a.deleteVersion(version); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
	}
}

func (a *API) handleUpdatesLatest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleUpdateLatest(w, r)
	case http.MethodPut, http.MethodPost:
		var body struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		v := normalizeVer(body.Version)
		if v == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "version required"})
			return
		}
		if _, _, err := a.loadVersionManifest(v); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		idx, err := a.loadIndex()
		if err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		found := false
		for _, e := range idx.Versions {
			if versionEqual(e.Version, v) {
				found = true
				break
			}
		}
		if !found {
			m, _, _ := a.loadVersionManifest(v)
			idx.Versions = append(idx.Versions, UpdateIndexEntry{
				Version: v, Notes: m.Notes, Force: m.Force, CreatedAt: m.CreatedAt,
			})
		}
		idx.Latest = v
		if err := a.saveIndex(idx); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latest": v})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
	}
}

func (a *API) deleteVersion(version string) error {
	v := normalizeVer(version)
	if v == "" {
		return fmt.Errorf("version required")
	}
	idx, err := a.loadIndex()
	if err != nil {
		return err
	}
	if versionEqual(idx.Latest, v) {
		return fmt.Errorf("cannot delete latest version; mark another as latest first")
	}
	removed := false
	for _, dir := range a.versionDirs(v) {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			removed = true
		}
	}
	if !removed {
		// Refuse deleting legacy flat root — too destructive.
		if flat, err := a.loadFlatManifest(); err == nil && versionEqual(flat.Version, v) {
			return fmt.Errorf("cannot delete legacy flat layout version; remove files manually or publish into versioned dirs")
		}
	}
	filtered := idx.Versions[:0]
	for _, e := range idx.Versions {
		if !versionEqual(e.Version, v) {
			filtered = append(filtered, e)
		}
	}
	idx.Versions = filtered
	return a.saveIndex(idx)
}

// handleUpdateLatest exposes the published latest agent version (compat path).
func (a *API) handleUpdateLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	idx, err := a.loadIndex()
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": "", "notes": ""})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	notes := ""
	force := false
	if m, _, err := a.loadVersionManifest(idx.Latest); err == nil {
		notes = m.Notes
		force = m.Force
	} else {
		for _, e := range idx.Versions {
			if versionEqual(e.Version, idx.Latest) {
				notes = e.Notes
				force = e.Force
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": idx.Latest, "notes": notes, "force": force,
	})
}

func (a *API) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	q := r.URL.Query()
	goos := strings.TrimSpace(q.Get("os"))
	goarch := strings.TrimSpace(q.Get("arch"))
	current := strings.TrimSpace(q.Get("version"))
	target := normalizeVer(q.Get("target"))
	if goos == "" {
		goos = "linux"
	}
	if goarch == "" {
		goarch = "amd64"
	}

	if target == "" {
		latest, err := a.latestVersion()
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, map[string]any{
					"ok": true, "update": false, "latest": "", "current": current,
					"error": "no update manifest published",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		target = latest
	}

	m, dir, fileName, art, err := a.resolveArtifact(target, goos, goarch)
	if err != nil {
		latest, _ := a.latestVersion()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "update": false, "latest": latest, "target": target, "current": current,
			"notes": m.Notes, "error": err.Error(),
		})
		return
	}
	_ = dir

	latest, _ := a.latestVersion()
	if latest == "" {
		latest = target
	}
	// target mode: apply when current != target (supports downgrade / pin).
	// default (latest): semver-less or force.
	need := !versionEqual(current, target)
	if normalizeVer(q.Get("target")) == "" {
		need = versionLess(current, target) || (m.Force && !versionEqual(current, target))
	}
	downloadPath := fmt.Sprintf("/api/agent/update/download/%s/%s/%s",
		target, strings.ToLower(goos), strings.ToLower(goarch))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"update":       need,
		"latest":       latest,
		"target":       target,
		"current":      current,
		"force":        m.Force,
		"notes":        m.Notes,
		"download_url": downloadPath,
		"file":         fileName,
		"size":         art.Size,
		"sha256":       art.SHA256,
	})
}

func (a *API) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	// /api/agent/update/download/{version}/{os}/{arch}
	// legacy: /api/agent/update/download/{os}/{arch}  → latest
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agent/update/download/"), "/")
	parts := strings.Split(rest, "/")
	var version, goos, goarch string
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "expected /download/{os}/{arch} or /download/{version}/{os}/{arch}"})
			return
		}
		latest, err := a.latestVersion()
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "no update manifest"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		version, goos, goarch = latest, parts[0], parts[1]
	case 3:
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "expected /download/{version}/{os}/{arch}"})
			return
		}
		version, goos, goarch = parts[0], parts[1], parts[2]
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "expected /download/{os}/{arch} or /download/{version}/{os}/{arch}"})
		return
	}

	m, dir, fileName, art, err := a.resolveArtifact(version, goos, goarch)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	full := filepath.Join(dir, fileName)
	f, err := os.Open(full)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("X-Proctor-Version", m.Version)
	if art.SHA256 != "" {
		w.Header().Set("X-Proctor-SHA256", art.SHA256)
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func (a *API) enqueueAgentUpdate(agentID, version string) (model.Command, error) {
	v := normalizeVer(version)
	if v == "" {
		latest, err := a.latestVersion()
		if err != nil {
			return model.Command{}, fmt.Errorf("version required (no latest published)")
		}
		v = latest
	}
	if _, _, err := a.loadVersionManifest(v); err != nil {
		return model.Command{}, err
	}
	cmd := model.Command{
		ID: common.NewID("cmd"), AgentID: agentID, Type: "update",
		Payload:   map[string]string{"version": v},
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := a.store.EnqueueCommand(cmd); err != nil {
		return model.Command{}, err
	}
	return cmd, nil
}

func (a *API) handleAgentUpdate(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cmd, err := a.enqueueAgentUpdate(agentID, body.Version)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd})
}

func (a *API) handleAgentsBatchUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	var body struct {
		AgentIDs  []string `json:"agent_ids"`
		Version   string   `json:"version"`
		Classroom string   `json:"classroom"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ids := body.AgentIDs
	if len(ids) == 0 && strings.TrimSpace(body.Classroom) != "" {
		list, err := a.store.ListAgents(a.online)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		want := strings.TrimSpace(body.Classroom)
		for _, ag := range list {
			if ag.Classroom == want {
				ids = append(ids, ag.ID)
			}
		}
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_ids or classroom required"})
		return
	}
	type item struct {
		AgentID string        `json:"agent_id"`
		OK      bool          `json:"ok"`
		Error   string        `json:"error,omitempty"`
		Command model.Command `json:"command,omitempty"`
	}
	items := make([]item, 0, len(ids))
	okCount := 0
	for _, id := range ids {
		cmd, err := a.enqueueAgentUpdate(id, body.Version)
		if err != nil {
			items = append(items, item{AgentID: id, OK: false, Error: err.Error()})
			continue
		}
		okCount++
		items = append(items, item{AgentID: id, OK: true, Command: cmd})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "queued": okCount, "total": len(ids), "results": items,
	})
}

// versionEqual / versionLess: prefer semver (major.minor.patch); fall back to string compare
// so ISO-ish build stamps from scripts still order correctly.
func versionEqual(a, b string) bool {
	return normalizeVer(a) == normalizeVer(b)
}

func versionLess(a, b string) bool {
	a, b = normalizeVer(a), normalizeVer(b)
	if a == "" {
		return b != ""
	}
	if b == "" {
		return false
	}
	ap, aOK := parseSemver(a)
	bp, bOK := parseSemver(b)
	if aOK && bOK {
		for i := 0; i < 3; i++ {
			if ap[i] < bp[i] {
				return true
			}
			if ap[i] > bp[i] {
				return false
			}
		}
		return false
	}
	return a < b
}

func normalizeVer(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	// strip build metadata / prerelease for classroom simplicity
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n := 0
		if parts[i] == "" {
			return out, false
		}
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				return out, false
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, true
}

// HashFileSHA256 is a small helper for operators publishing updates.
func HashFileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
