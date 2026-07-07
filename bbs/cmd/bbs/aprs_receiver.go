package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *app) runAPRSReceiver() error {
	login := a.aprsReceiverLogin()
	if !validAPRSCallsign(login) {
		return fmt.Errorf("invalid APRS receiver callsign: %s", a.cfg.aprsReceiverCallsign)
	}
	passcode := aprsPasscode(login)
	restartAt := time.Now().Add(aprsReceiverRestartInterval)
	a.logAPRSReceiver("APRS receiver starting login=%s server=%s:%d filter=t/m restart_at=%s", login, a.cfg.aprsServer, a.cfg.aprsPort, restartAt.UTC().Format("2006-01-02 15:04 UTC"))
	for {
		if !time.Now().Before(restartAt) {
			a.logAPRSReceiver("APRS receiver hourly restart requested")
			return nil
		}
		if err := a.receiveAPRSLoop(login, passcode, restartAt); err != nil {
			a.logAPRSReceiver("APRS receiver disconnected: %v", err)
		}
		if !time.Now().Before(restartAt) {
			a.logAPRSReceiver("APRS receiver hourly restart requested")
			return nil
		}
		time.Sleep(minDuration(10*time.Second, time.Until(restartAt)))
	}
}

func (a *app) aprsReceiverLogin() string {
	configured := aprsBaseCallsign(a.cfg.aprsReceiverCallsign)
	if configured != "" && validAPRSCallsign(configured) {
		return configured
	}
	for _, callsign := range sortedKeys(a.cfg.sysops) {
		base := aprsBaseCallsign(callsign)
		if validAPRSCallsign(base) {
			return base
		}
	}
	return "N0CALL"
}

func (a *app) receiveAPRSLoop(login string, passcode int, restartAt time.Time) error {
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(restartAt)
	a.logAPRSReceiver("APRS receiver connected to %s", address)
	if _, err := fmt.Fprintf(conn, "user %s pass %d vers HamNetBBS 0.1 filter t/m\r\n", login, passcode); err != nil {
		return err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			a.logAPRSReceiver("APRS-IS: %s", line)
			continue
		}
		msg, ok := parseAPRSMessageLine(line)
		if !ok {
			continue
		}
		user, saved, err := a.storeReceivedAPRS(msg)
		if err != nil {
			a.logAPRSReceiver("APRS receiver store error from=%s to=%s: %v", msg.From, msg.To, err)
			continue
		}
		if saved {
			a.logAPRSReceiver("APRS received message user=%s from=%s to=%s text=%s", user, msg.From, msg.To, msg.Text)
		}
	}
	return scanner.Err()
}

func parseAPRSMessageLine(raw string) (receivedAPRS, bool) {
	header, info, ok := strings.Cut(raw, ":")
	if !ok || !strings.HasPrefix(info, ":") || len(info) < 11 || info[10] != ':' {
		return receivedAPRS{}, false
	}
	source, _, ok := strings.Cut(header, ">")
	if !ok {
		return receivedAPRS{}, false
	}
	to := strings.TrimSpace(info[1:10])
	text := stripAPRSMessageID(strings.TrimSpace(info[11:]))
	if to == "" || text == "" {
		return receivedAPRS{}, false
	}
	return receivedAPRS{
		At:   now(),
		From: normalizeAPRSCallsign(source),
		To:   normalizeAPRSCallsign(to),
		Text: text,
		Raw:  raw,
	}, true
}

func (a *app) storeReceivedAPRS(msg receivedAPRS) (string, bool, error) {
	msg.Text = stripAPRSMessageID(msg.Text)
	users, err := a.loadUsers()
	if err != nil {
		return "", false, err
	}
	key := aprsRecipientKey(msg.To)
	profile, ok := users[key]
	if !ok || profile.Disabled || !profile.EnableAPRS {
		return key, false, nil
	}
	all := map[string][]receivedAPRS{}
	if err := readJSON(a.cfg.aprsReceivedFile, &all, map[string][]receivedAPRS{}); err != nil {
		return key, false, err
	}
	history := all[key]
	for i := len(history) - 1; i >= 0 && i >= len(history)-20; i-- {
		if history[i].Raw == msg.Raw {
			return key, false, nil
		}
	}
	history = append(history, msg)
	if len(history) > receivedHistoryLimit {
		history = history[len(history)-receivedHistoryLimit:]
	}
	all[key] = history
	return key, true, writeJSON(a.cfg.aprsReceivedFile, all)
}

func (a *app) normalizeReceivedAPRSStore() error {
	all := map[string][]receivedAPRS{}
	if err := readJSON(a.cfg.aprsReceivedFile, &all, map[string][]receivedAPRS{}); err != nil {
		return err
	}
	changed := false
	for key, history := range all {
		for i := range history {
			cleaned := stripAPRSMessageID(history[i].Text)
			if cleaned != history[i].Text {
				history[i].Text = cleaned
				changed = true
			}
		}
		all[key] = history
	}
	if !changed {
		return nil
	}
	return writeJSON(a.cfg.aprsReceivedFile, all)
}

func aprsRecipientKey(to string) string {
	return normalizeCallsign(aprsBaseCallsign(to))
}

func stripAPRSMessageID(text string) string {
	text = strings.TrimSpace(text)
	idx := strings.LastIndex(text, "{")
	if idx < 0 {
		return text
	}
	id := text[idx+1:]
	if id == "" || len([]rune(id)) > 5 {
		return text
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			if r < 'A' || r > 'Z' {
				if r < 'a' || r > 'z' {
					return text
				}
			}
		}
	}
	return strings.TrimSpace(text[:idx])
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (a *app) logAPRSReceiver(format string, args ...any) {
	text := fmt.Sprintf("%s %s\n", now(), fmt.Sprintf(format, args...))
	if err := os.MkdirAll(filepath.Dir(a.cfg.aprsReceiverLogFile), 0o755); err == nil {
		if file, err := os.OpenFile(a.cfg.aprsReceiverLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = file.WriteString(text)
			_ = file.Close()
			return
		}
	}
	if err := os.WriteFile("/proc/1/fd/1", []byte(text), 0o600); err == nil {
		return
	}
	_, _ = fmt.Fprint(os.Stderr, text)
}
