package main

import (
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
)

var supervisorOutputMu sync.Mutex

func (a *app) runAPRSSupervisor() error {
	if err := a.ensureRuntimeLogFiles(); err != nil {
		return err
	}
	go tailLogFile(a.cfg.aprsLogFile)
	go tailLogFile(a.cfg.bbsLogFile)
	go tailLogFile(a.cfg.authLogFile)
	go tailLogFile(a.cfg.fail2banLogFile)
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
	return []string{a.cfg.aprsLogFile, a.cfg.bbsLogFile, a.cfg.authLogFile, a.cfg.fail2banLogFile}
}

func (a *app) watchAPRSReceiver() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	for {
		cmd := exec.Command(exe, "aprs-receiver")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			a.logAPRSReceiver("APRS receiver watchdog start error: %v; retrying in %s", err, aprsReceiverRetryDelay)
			time.Sleep(aprsReceiverRetryDelay)
			continue
		}
		err := cmd.Wait()
		if err != nil {
			a.logAPRSReceiver("APRS receiver exited with error: %v; restarting in %s", err, aprsReceiverRetryDelay)
		} else {
			a.logAPRSReceiver("APRS receiver exited normally; restarting in %s", aprsReceiverRetryDelay)
		}
		time.Sleep(aprsReceiverRetryDelay)
	}
}

func (a *app) rotateRuntimeLogsNightly() {
	for {
		time.Sleep(durationUntilNextLogRotation(time.Now().UTC()))
		if err := a.rotateRuntimeLogs(time.Now().UTC()); err != nil {
			a.logAPRSReceiver("Log rotation error: %v", err)
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

func rotateLog(path string, now time.Time) (string, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	active, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", false, err
	}
	defer active.Close()

	info, err := active.Stat()
	if err != nil {
		return "", false, err
	}
	if info.Size() == 0 {
		return "", false, nil
	}
	if _, err := active.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	archive := nextLogArchivePath(path, now)
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", false, err
	}
	if _, err := io.Copy(out, active); err != nil {
		_ = out.Close()
		return "", false, err
	}
	if err := out.Close(); err != nil {
		return "", false, err
	}
	if err := active.Truncate(0); err != nil {
		return "", false, err
	}
	if _, err := active.Seek(0, io.SeekStart); err != nil {
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
