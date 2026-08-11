package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchivedLogPath(t *testing.T) {
	got := archivedLogPath(filepath.Join("logs", "agent.log"), "2026-08-10")
	want := filepath.Join("logs", "agent.2026-08-10.log")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestOpenDailyLogCreatesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")

	// Pretend yesterday's log already exists.
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	if err := os.Chtimes(path, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}

	w, err := openDailyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}

	day := yesterday.Format("2006-01-02")
	archived := archivedLogPath(path, day)
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("expected archived log %s: %v", archived, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new\n" {
		t.Fatalf("current log content = %q", b)
	}
}

func TestIsUnstableCWD(t *testing.T) {
	if !isUnstableCWD("/") {
		t.Fatal("root should be unstable")
	}
	if isUnstableCWD(t.TempDir()) {
		t.Fatal("temp dir should be stable")
	}
}
