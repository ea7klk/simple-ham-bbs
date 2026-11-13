package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sharedDirApp builds an app on an explicit directory, so two of them can contend
// for the same reception lock the way two pods do.
func sharedDirApp(t *testing.T, dir string) *app {
	t.Helper()
	dbFile := filepath.Join(dir, "bbs.sqlite")
	db, err := openDatabase(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return &app{cfg: config{
		dataDir:     dir,
		dbFile:      dbFile,
		aprsLogFile: filepath.Join(dir, "aprs", "aprs.log"),
		bbsLogFile:  filepath.Join(dir, "bbs.log"),
		authLogFile: filepath.Join(dir, "auth.log"),
	}, db: db, text: map[string]map[string]any{"en": {}}}
}

// Only one process may receive. Two would write the same rows and both
// acknowledge the same message, which puts a duplicate ACK on the air under the
// operator's callsign.
func TestAPRSReceptionIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first := sharedDirApp(t, dir)

	lock, err := first.claimAPRSReception()
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	second := sharedDirApp(t, dir)
	acquired := make(chan struct{})
	go func() {
		lock2, err := second.claimAPRSReception()
		if err != nil {
			t.Errorf("second claim: %v", err)
			close(acquired)
			return
		}
		_ = unlockFile(lock2)
		_ = lock2.Close()
		close(acquired)
	}()

	// While the first holds the lock the second must not get it.
	select {
	case <-acquired:
		t.Fatal("a second receiver took the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}

	// Releasing hands it over.
	if err := unlockFile(lock); err != nil {
		t.Fatal(err)
	}
	_ = lock.Close()

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the standby never acquired the lock after it was released")
	}
}

// A standby waits rather than failing, so a rolling update hands over instead of
// leaving a gap in reception.
func TestAPRSReceptionStandbyWaits(t *testing.T) {
	dir := t.TempDir()
	first := sharedDirApp(t, dir)

	lock, err := first.claimAPRSReception()
	if err != nil {
		t.Fatal(err)
	}

	second := sharedDirApp(t, dir)
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	go func() {
		defer wg.Done()
		lock2, err := second.claimAPRSReception()
		if err != nil {
			t.Errorf("standby claim: %v", err)
			return
		}
		if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
			t.Errorf("standby took the lock after %s; it did not wait", elapsed)
		}
		_ = unlockFile(lock2)
		_ = lock2.Close()
	}()

	time.Sleep(400 * time.Millisecond)
	_ = unlockFile(lock)
	_ = lock.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the standby did not proceed after the lock was released")
	}
}

// The lock lives on the shared volume, not in the container, or it would not be
// held across pods.
func TestAPRSReceptionLockIsOnTheSharedVolume(t *testing.T) {
	dir := t.TempDir()
	a := sharedDirApp(t, dir)
	want := filepath.Join(dir, ".aprs-reception.lock")
	if got := a.aprsReceptionLockPath(); got != want {
		t.Fatalf("lock path = %q, want %q", got, want)
	}
	lock, err := a.claimAPRSReception()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlockFile(lock); _ = lock.Close() }()
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the lock file was not created on the volume: %v", err)
	}
}

// The supervisor must take the lock before doing any work. This is the wiring
// check: a standby has to be idle, not a second receiver that happens to write
// somewhere else.
func TestSupervisorClaimsReceptionBeforeStarting(t *testing.T) {
	dir := t.TempDir()
	holder := sharedDirApp(t, dir)
	lock, err := holder.claimAPRSReception()
	if err != nil {
		t.Fatal(err)
	}

	standby := sharedDirApp(t, dir)
	// Stand in for the receiver loop so the test asserts the lock ordering without
	// spawning another copy of this test binary.
	running := make(chan struct{}, 1)
	standby.receiverRunner = func() error {
		running <- struct{}{}
		return nil
	}
	go func() { _ = standby.runAPRSSupervisor() }()

	// While the lock is held the standby must only have announced that it is
	// waiting. "supervisor started" is written after claiming, so its absence is
	// what proves no second receiver began.
	time.Sleep(500 * time.Millisecond)
	logged := readFileOrEmpty(standby.cfg.aprsLogFile)
	if strings.Contains(logged, "APRS supervisor started") {
		t.Fatalf("the standby started a supervisor without the lock:\n%s", logged)
	}
	if !strings.Contains(logged, "standing by") {
		t.Fatalf("the standby did not say it was waiting:\n%s", logged)
	}
	select {
	case <-running:
		t.Fatal("the standby started the receiver without the lock")
	default:
	}

	_ = unlockFile(lock)
	_ = lock.Close()
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor did not start reception after the lock was released")
	}
	if !strings.Contains(readFileOrEmpty(standby.cfg.aprsLogFile), "handed over") {
		t.Fatal("the standby never recorded that it took over")
	}
}

// readFileOrEmpty returns the file's contents, or "" when it does not exist.
func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
