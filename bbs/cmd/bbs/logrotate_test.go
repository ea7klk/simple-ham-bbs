package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Renaming cannot drop an append: a writer either lands in the file before the
// rename (and ends up in the archive) or opens the freshly created active file
// afterwards.
func TestRotateRenamesAndRecreatesActiveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bbs.log")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)

	archive, rotated, err := rotateLog(path, at)
	if err != nil || !rotated {
		t.Fatalf("rotateLog() archive=%q rotated=%v err=%v", archive, rotated, err)
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "first\nsecond\n" {
		t.Fatalf("archive body = %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("active log was not recreated: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("recreated active log is not empty: %d bytes", info.Size())
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("recreated active log permissions = %o", perm)
	}
}

// Appends after a rotation land in the new active file, not the archive.
func TestAppendAfterRotationGoesToNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bbs.log")
	a := &app{cfg: config{bbsLogFile: path}}
	at := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)

	a.logBBSAction("EA7KLK", "before", "%s", "")
	archive, rotated, err := rotateLog(path, at)
	if err != nil || !rotated {
		t.Fatalf("rotateLog() rotated=%v err=%v", rotated, err)
	}
	a.logBBSAction("EA7KLK", "after", "%s", "")

	archived, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archived), "action=before") {
		t.Fatalf("archive missing the pre-rotation line: %q", archived)
	}
	if strings.Contains(string(archived), "action=after") {
		t.Fatalf("post-rotation line went to the archive: %q", archived)
	}
	if !strings.Contains(string(active), "action=after") {
		t.Fatalf("post-rotation line missing from the active log: %q", active)
	}
	if strings.Count(string(active), "action=before") != 0 {
		t.Fatalf("active log duplicated pre-rotation content: %q", active)
	}
}

// An empty log rotates to nothing and leaves no empty archive behind.
func TestRotateEmptyLogIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aprs.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)
	archive, rotated, err := rotateLog(path, at)
	if err != nil || rotated || archive != "" {
		t.Fatalf("rotateLog() archive=%q rotated=%v err=%v", archive, rotated, err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "aprs.*.log"))
	if len(matches) != 0 {
		t.Fatalf("empty log created an archive: %v", matches)
	}
}

// Two rotations racing must not both claim the same archive name or lose data.
func TestConcurrentRotationsDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bbs.log")
	if err := os.WriteFile(path, []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)

	const workers = 4
	var wg sync.WaitGroup
	archives := make([]string, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			archive, rotated, err := rotateLog(path, at)
			errs[i] = err
			if rotated {
				archives[i] = archive
			}
		}(i)
	}
	close(start)
	wg.Wait()

	rotated := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if archives[i] != "" {
			rotated++
			body, readErr := os.ReadFile(archives[i])
			if readErr != nil {
				t.Fatalf("archive %s unreadable: %v", archives[i], readErr)
			}
			if string(body) != "payload\n" {
				t.Fatalf("archive %s body = %q", archives[i], body)
			}
		}
	}
	if rotated != 1 {
		t.Fatalf("%d rotations claimed the same content, want exactly 1", rotated)
	}
	// The active file must exist and be empty for appenders.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("active log missing after concurrent rotation: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("active log size = %d, want 0", info.Size())
	}
}

// The tailer resets its offset when the active file is replaced, and does not
// re-emit content that moved into the archive.
func TestTailFollowsRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bbs.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	offset, err := copyLogAppend(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	next, err := copyLogAppend(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if next != 0 {
		t.Fatalf("offset after rotation = %d, want 0", next)
	}
}

// A child that outlives the deadline is killed, and the watchdog gets control
// back instead of blocking in Wait forever.
func TestRunSupervisedProcessKillsWedgedChild(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	// Use real files for the child's output, as the production caller does with
	// os.Stdout: a non-file writer makes exec spawn a copying goroutine, and
	// then WaitDelay governs how long Wait keeps waiting on it.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	start := time.Now()
	timedOut, err := runSupervisedProcess([]string{"/bin/sh", "-c", "sleep 30"}, 200*time.Millisecond, devNull, devNull)
	elapsed := time.Since(start)

	if !timedOut {
		t.Fatalf("timedOut = false, err = %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("kill took %s; the watchdog would still be blocked", elapsed)
	}
}

func TestRunSupervisedProcessReportsNormalExit(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	timedOut, err := runSupervisedProcess([]string{"/bin/sh", "-c", "exit 0"}, 5*time.Second, io.Discard, io.Discard)
	if timedOut {
		t.Fatal("a quick exit was reported as a timeout")
	}
	if err != nil {
		t.Fatalf("normal exit returned %v", err)
	}
}

func TestRunSupervisedProcessReportsFailure(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	timedOut, err := runSupervisedProcess([]string{"/bin/sh", "-c", "exit 3"}, 5*time.Second, io.Discard, io.Discard)
	if timedOut {
		t.Fatal("a failing exit was reported as a timeout")
	}
	if err == nil {
		t.Fatal("non-zero exit returned no error")
	}
}

func TestRunSupervisedProcessReportsStartError(t *testing.T) {
	if _, err := runSupervisedProcess([]string{"/nonexistent/bbs-test-binary"}, time.Second, io.Discard, io.Discard); err == nil {
		t.Fatal("starting a missing binary returned no error")
	}
}

// The receiver gets a longer ceiling than its own hourly restart, or the
// watchdog would kill healthy processes.
func TestReceiverRuntimeCeilingExceedsSelfRestart(t *testing.T) {
	if aprsReceiverMaxRuntime <= aprsReceiverRestartInterval {
		t.Fatalf("ceiling %s does not exceed the receiver's own %s restart", aprsReceiverMaxRuntime, aprsReceiverRestartInterval)
	}
}
