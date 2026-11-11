package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	logRetention           = 7 * 24 * time.Hour
	aprsReceiverRetryDelay = 10 * time.Second
	// aprsReceiverMaxRuntime is a hard ceiling on one receiver process. The
	// receiver restarts itself after an hour, so anything still running past
	// this is wedged — stuck in a dial, a write, or a filesystem call — and the
	// watchdog has to break the deadlock rather than wait forever.
	aprsReceiverMaxRuntime = 75 * time.Minute
	// aprsReceiverWaitDelay bounds how long Wait keeps waiting for the child's
	// output to drain after the kill, so a stuck pipe cannot stall the watchdog.
	aprsReceiverWaitDelay = 10 * time.Second
)

var supervisorOutputMu sync.Mutex

func (a *app) runAPRSSupervisor() error {
	if err := a.ensureRuntimeLogFiles(); err != nil {
		return err
	}
	go tailLogFile(a.cfg.aprsLogFile)
	go tailLogFile(a.cfg.bbsLogFile)
	go tailLogFile(a.cfg.authLogFile)
	go a.rotateRuntimeLogsNightly()

	a.logAPRSReceiver("APRS supervisor started; nightly log rotation at 03:00 UTC, retention=7 days")
	return a.watchAPRSReceiver()
}

func (a *app) ensureRuntimeLogFiles() error {
	for _, path := range a.runtimeLogFiles() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_ = file.Close()
	}
	return nil
}

func (a *app) runtimeLogFiles() []string {
	return []string{a.cfg.aprsLogFile, a.cfg.bbsLogFile, a.cfg.authLogFile}
}

func (a *app) watchAPRSReceiver() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	for {
		timedOut, err := runSupervisedProcess([]string{exe, "aprs-receiver"}, aprsReceiverMaxRuntime, os.Stdout, os.Stderr)
		switch {
		case timedOut:
			a.logAPRSReceiver("APRS receiver exceeded %s; killed and restarting in %s", aprsReceiverMaxRuntime, aprsReceiverRetryDelay)
		case err != nil:
			a.logAPRSReceiver("APRS receiver exited with error: %v; restarting in %s", err, aprsReceiverRetryDelay)
		default:
			a.logAPRSReceiver("APRS receiver exited normally; restarting in %s", aprsReceiverRetryDelay)
		}
		time.Sleep(aprsReceiverRetryDelay)
	}
}

// runSupervisedProcess runs argv, killing it if it outlives timeout, and waits
// for it either way.
//
// A plain cmd.Wait() blocks until the child exits, so a receiver wedged in a
// dial kept the watchdog waiting indefinitely — the hourly self-restart only
// helps a child that is still making progress. The context puts a hard ceiling
// on one run; WaitDelay bounds the wait for output to drain afterwards.
func runSupervisedProcess(argv []string, timeout time.Duration, stdout, stderr io.Writer) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = aprsReceiverWaitDelay
	if err := cmd.Start(); err != nil {
		return false, err
	}
	err := cmd.Wait()
	return ctx.Err() == context.DeadlineExceeded, err
}

func (a *app) rotateRuntimeLogsNightly() {
	for {
		time.Sleep(durationUntilNextLogRotation(time.Now().UTC()))
		if err := a.rotateRuntimeLogs(time.Now().UTC()); err != nil {
			a.logAPRSReceiver("Log rotation error: %v", err)
		}
		// Nightly housekeeping rides the same timer: expire stale login-throttle
		// counters so the table cannot grow without bound.
		if err := a.pruneAuthFailures(time.Now().UTC()); err != nil {
			a.logAPRSReceiver("Login throttle prune error: %v", err)
		}
		if err := a.pruneRegistrations(time.Now().UTC()); err != nil {
			a.logAPRSReceiver("Registration throttle prune error: %v", err)
		}
	}
}

func durationUntilNextLogRotation(now time.Time) time.Duration {
	target := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	delay := target.Sub(now)
	if delay < time.Second {
		return time.Second
	}
	return delay
}

func (a *app) rotateRuntimeLogs(now time.Time) error {
	for _, path := range a.runtimeLogFiles() {
		archive, rotated, err := rotateLog(path, now)
		if err != nil {
			return err
		}
		if rotated {
			a.logAPRSReceiver("Rotated log %s to %s", path, archive)
		}
		if err := removeOldLogArchives(path, now); err != nil {
			return err
		}
	}
	return nil
}

// rotateLog archives the active file by renaming it and creating a fresh one.
//
// The previous implementation copied the file and then truncated it, so every
// append that landed between the copy and the truncate was lost — and appends
// come from other processes (the receiver, every BBS session). Renaming has no
// such window: a writer either appends before the rename (into the archive,
// which is correct) or opens the newly created active file afterwards. Every
// append goes through a fresh open, so no writer holds the old inode across the
// switch.
//
// An exclusive lock on the active file serialises concurrent rotations, which
// can overlap when the supervisor restarts.
func rotateLog(path string, now time.Time) (string, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	// Lock a dedicated lock file, not the log itself: the log's inode is
	// replaced by the rename below, and a lock belongs to the inode. Locking the
	// log would let a waiter wake up holding the archived file and rotate an
	// empty one under a second name.
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", false, err
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return "", false, err
	}
	defer unlockFile(lock)

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		// Nothing to rotate; make sure the path exists for appenders.
		fresh, createErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if createErr != nil {
			return "", false, createErr
		}
		return "", false, fresh.Close()
	}
	if err != nil {
		return "", false, err
	}
	if info.Size() == 0 {
		return "", false, nil
	}
	archive := nextLogArchivePath(path, now)
	if err := os.Rename(path, archive); err != nil {
		return "", false, err
	}
	fresh, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		// Put the archived file back rather than leave appenders without a path.
		_ = os.Rename(archive, path)
		return "", false, err
	}
	if err := fresh.Close(); err != nil {
		return "", false, err
	}
	return archive, true, nil
}

func nextLogArchivePath(path string, now time.Time) string {
	dir := filepath.Dir(path)
	base := logBase(path)
	stamp := now.UTC().Format("2006-01-02")
	archive := filepath.Join(dir, fmt.Sprintf("%s.%s.log", base, stamp))
	for suffix := 1; exists(archive); suffix++ {
		archive = filepath.Join(dir, fmt.Sprintf("%s.%s.%d.log", base, stamp, suffix))
	}
	return archive
}

func removeOldLogArchives(activePath string, now time.Time) error {
	pattern := filepath.Join(filepath.Dir(activePath), logBase(activePath)+".*.log")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	cutoff := now.Add(-logRetention)
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func logBase(path string) string {
	return filepath.Base(path[:len(path)-len(filepath.Ext(path))])
}

func tailLogFile(path string) {
	supervisorOutputMu.Lock()
	fmt.Fprintf(os.Stdout, "==> %s <==\n", path)
	supervisorOutputMu.Unlock()

	offset := seekLogEnd(path)
	for {
		next, err := copyLogAppend(path, offset)
		if err == nil {
			offset = next
		}
		time.Sleep(time.Second)
	}
}

func seekLogEnd(path string) int64 {
	for {
		info, err := os.Stat(path)
		if err == nil {
			return info.Size()
		}
		time.Sleep(time.Second)
	}
}

func copyLogAppend(path string, offset int64) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset {
		return offset, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	supervisorOutputMu.Lock()
	written, err := io.Copy(os.Stdout, file)
	supervisorOutputMu.Unlock()
	return offset + written, err
}
