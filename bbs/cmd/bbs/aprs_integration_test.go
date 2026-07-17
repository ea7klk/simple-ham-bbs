package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func withFakeAPRSIS(t *testing.T, expectedPackets int) <-chan []string {
	t.Helper()
	original := netDialTimeout
	packets := make(chan []string, 1)
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		go func() {
			defer close(packets)
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(server)
			login, err := reader.ReadString('\n')
			if err != nil {
				packets <- []string{"login-error:" + err.Error()}
				return
			}
			_, _ = server.Write([]byte("# aprsc test server\r\n# logresp TEST verified, server TEST\r\n"))
			out := []string{strings.TrimSpace(login)}
			for len(out)-1 < expectedPackets {
				line, err := reader.ReadString('\n')
				if err != nil {
					out = append(out, "packet-error:"+err.Error())
					break
				}
				out = append(out, strings.TrimSpace(line))
			}
			packets <- out
		}()
		return client, nil
	}
	t.Cleanup(func() { netDialTimeout = original })
	return packets
}

func TestSendAPRSMessageViaFakeServer(t *testing.T) {
	packets := withFakeAPRSIS(t, 1)
	a := testApp(t)
	a.cfg.aprsServer = "127.0.0.1"
	a.cfg.aprsPort = 14580
	a.cfg.aprsLogFile = filepath.Join(t.TempDir(), "aprs.log")

	sent, ok := a.sendAPRSMessage("ea7klk", "ea1abc-0", "Hello APRS", "en")
	if !ok || sent.Status != aprsStatusSent || len(sent.Parts) != 1 || sent.Parts[0].MessageID == "" {
		t.Fatalf("sendAPRSMessage sent=%#v ok=%v", sent, ok)
	}
	got := <-packets
	if len(got) != 2 || !strings.Contains(got[0], "user EA7KLK-0 pass") || !strings.Contains(got[1], "EA7KLK-0>APRS,TCPIP*::EA1ABC-0 :Hello APRS{") {
		t.Fatalf("fake APRS-IS packets = %#v", got)
	}
	body, err := os.ReadFile(a.cfg.aprsLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "APRS-IS send-message") || !strings.Contains(string(body), "Hello APRS") {
		t.Fatalf("APRS send log = %s", body)
	}
}

func TestSendAPRSAckAndBeaconViaFakeServer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		send    func(*app) error
		want    string
		wantLog string
	}{
		{
			name: "ack",
			send: func(a *app) error {
				return a.sendAPRSAck("ea7klk-0", "ea1abc-0", "a1")
			},
			want:    "EA7KLK-0>APRS,TCPIP*::EA1ABC-0 :ackA1",
			wantLog: "ackA1",
		},
		{
			name: "beacon",
			send: func(a *app) error {
				return a.sendAPRSBeacon("ea7klk-0", 37.3125, -5.9583333333, "HamNet BBS")
			},
			want:    `EA7KLK-0>APRS,TCPIP*:!3718.75N\00557.50WmHamNet BBS`,
			wantLog: "BEACON",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packets := withFakeAPRSIS(t, 1)
			a := testApp(t)
			a.cfg.aprsServer = "127.0.0.1"
			a.cfg.aprsPort = 14580
			a.cfg.aprsLogFile = filepath.Join(t.TempDir(), "aprs.log")
			if err := tc.send(a); err != nil {
				t.Fatal(err)
			}
			got := <-packets
			if len(got) != 2 || got[1] != tc.want {
				t.Fatalf("packet = %#v, want %q", got, tc.want)
			}
			body, err := os.ReadFile(a.cfg.aprsLogFile)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), tc.wantLog) {
				t.Fatalf("log missing %q: %s", tc.wantLog, body)
			}
		})
	}
}

func TestSendLoginAPRSBeaconUsesUserCallsignSSID0(t *testing.T) {
	packets := withFakeAPRSIS(t, 1)
	a := testApp(t)
	a.cfg.aprsServer = "127.0.0.1"
	a.cfg.aprsPort = 14580
	dir := t.TempDir()
	a.cfg.aprsLogFile = filepath.Join(dir, "aprs.log")
	a.cfg.bbsLogFile = filepath.Join(dir, "bbs.log")

	a.sendLoginAPRSBeacon("ea7klk", userProfile{EnableAPRS: true, Maidenhead: "IM77AH"})

	got := <-packets
	if len(got) != 2 || !strings.HasPrefix(got[1], `EA7KLK-0>APRS,TCPIP*:!3718.75N\00557.50Wm`) {
		t.Fatalf("login beacon packet = %#v", got)
	}
	body, err := os.ReadFile(a.cfg.bbsLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "aprs_beacon_sent user=EA7KLK") {
		t.Fatalf("login beacon BBS log = %s", body)
	}
}

func TestSendLoginAPRSBeaconSkipsAndLogs(t *testing.T) {
	a := testApp(t)
	dir := t.TempDir()
	a.cfg.aprsLogFile = filepath.Join(dir, "aprs.log")
	a.cfg.bbsLogFile = filepath.Join(dir, "bbs.log")
	a.sendLoginAPRSBeacon("invalid callsign!", userProfile{EnableAPRS: true, Maidenhead: "IM77AH"})
	a.sendLoginAPRSBeacon("EA7KLK", userProfile{EnableAPRS: true, Maidenhead: "bad"})
	body, err := os.ReadFile(a.cfg.bbsLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "aprs_beacon_skipped") != 2 {
		t.Fatalf("beacon skip log = %s", body)
	}
	a.sendLoginAPRSBeacon("EA7KLK", userProfile{EnableAPRS: false, Maidenhead: "IM77AH"})
}

func TestReadAPRSISLoginResponseAndPipeWrite(t *testing.T) {
	okReader := bufio.NewReader(strings.NewReader("# hello\n# logresp EA7KLK verified\n"))
	resp, err := readAPRSISLoginResponse(okReader)
	if err != nil || !strings.Contains(resp, "verified") {
		t.Fatalf("readAPRSISLoginResponse ok resp=%q err=%v", resp, err)
	}
	badReader := bufio.NewReader(strings.NewReader("# logresp EA7KLK unverified\n"))
	if _, err := readAPRSISLoginResponse(badReader); err == nil {
		t.Fatal("unverified login response returned nil error")
	}
	missingReader := bufio.NewReader(strings.NewReader("# line\n"))
	if _, err := readAPRSISLoginResponse(missingReader); err == nil {
		t.Fatal("missing logresp returned nil error")
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		line, _ := bufio.NewReader(server).ReadString('\n')
		if strings.TrimSpace(line) != "PACKET" {
			t.Errorf("pipe received %q", line)
		}
	}()
	if err := writeAPRSISPacket(client, "PACKET"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestAPRSReceiverRecipientAndCleanupHelpers(t *testing.T) {
	a := testApp(t)
	if err := a.saveUsers(map[string]userProfile{
		"EA7KLK": {EnableAPRS: true},
		"EA1OFF": {EnableAPRS: false},
		"EA2DIS": {EnableAPRS: true, Disabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		to      string
		enabled bool
		exists  bool
	}{
		{"EA7KLK-0", true, true},
		{"EA1OFF-7", false, true},
		{"EA2DIS", false, true},
		{"EA9NOPE", false, false},
	} {
		user, enabled, err := a.aprsRecipientEnabled(tc.to)
		if err != nil {
			t.Fatal(err)
		}
		if user != aprsRecipientKey(tc.to) || enabled != tc.enabled {
			t.Fatalf("aprsRecipientEnabled(%q) = %q/%v", tc.to, user, enabled)
		}
		user, exists, err := a.aprsRecipientExists(tc.to)
		if err != nil {
			t.Fatal(err)
		}
		if user != aprsRecipientKey(tc.to) || exists != tc.exists {
			t.Fatalf("aprsRecipientExists(%q) = %q/%v", tc.to, user, exists)
		}
	}

	oldKey := "EA1|EA2|2"
	newKey := "EA3|EA4|2"
	buffers := map[string]*aprsMultipartBuffer{
		oldKey: {Updated: time.Now().Add(-time.Hour)},
		newKey: {Updated: time.Now()},
	}
	pruneAPRSMessageParts(buffers, 30*time.Minute)
	if _, ok := buffers[oldKey]; ok {
		t.Fatal("old multipart buffer was not pruned")
	}
	if _, ok := buffers[newKey]; !ok {
		t.Fatal("new multipart buffer was pruned")
	}
	if minDuration(time.Second, time.Minute) != time.Second {
		t.Fatal("minDuration failed")
	}
}

func TestAPRSLogRotationAndSupervisorHelpers(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{
		aprsLogFile:     filepath.Join(dir, "aprs", "aprs.log"),
		bbsLogFile:      filepath.Join(dir, "bbs.log"),
		authLogFile:     filepath.Join(dir, "auth.log"),
		fail2banLogFile: filepath.Join(dir, "fail2ban.log"),
	}}
	if err := a.ensureRuntimeLogFiles(); err != nil {
		t.Fatal(err)
	}
	for _, path := range a.runtimeLogFiles() {
		if !exists(path) {
			t.Fatalf("runtime log file missing: %s", path)
		}
	}
	nowTime := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	if durationUntilNextLogRotation(nowTime) != 24*time.Hour {
		t.Fatalf("duration at rotation boundary = %s", durationUntilNextLogRotation(nowTime))
	}
	before := time.Date(2026, 7, 10, 2, 59, 0, 0, time.UTC)
	if durationUntilNextLogRotation(before) != time.Minute {
		t.Fatalf("duration before rotation = %s", durationUntilNextLogRotation(before))
	}
	if logBase(a.cfg.aprsLogFile) != "aprs" {
		t.Fatalf("logBase = %q", logBase(a.cfg.aprsLogFile))
	}
	if got := len(a.runtimeLogFiles()); got != 4 {
		t.Fatalf("runtimeLogFiles count = %d, want 4", got)
	}

	if archive, rotated, err := rotateLog(a.cfg.aprsLogFile, nowTime); err != nil || rotated || archive != "" {
		t.Fatalf("empty rotateLog archive=%q rotated=%v err=%v", archive, rotated, err)
	}
	if err := os.WriteFile(a.cfg.aprsLogFile, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, rotated, err := rotateLog(a.cfg.aprsLogFile, nowTime)
	if err != nil || !rotated {
		t.Fatalf("rotateLog archive=%q rotated=%v err=%v", archive, rotated, err)
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "line1\nline2\n" {
		t.Fatalf("archive body = %q", body)
	}
	active, _ := os.ReadFile(a.cfg.aprsLogFile)
	if len(active) != 0 {
		t.Fatalf("active log not truncated: %q", active)
	}
	if next := nextLogArchivePath(a.cfg.aprsLogFile, nowTime); !strings.Contains(next, ".1.log") {
		t.Fatalf("next archive did not add suffix: %s", next)
	}

	oldArchive := filepath.Join(filepath.Dir(a.cfg.aprsLogFile), "aprs.2026-01-01.log")
	if err := os.WriteFile(oldArchive, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := nowTime.Add(-logRetention - time.Hour)
	if err := os.Chtimes(oldArchive, old, old); err != nil {
		t.Fatal(err)
	}
	if err := removeOldLogArchives(a.cfg.aprsLogFile, nowTime); err != nil {
		t.Fatal(err)
	}
	if exists(oldArchive) {
		t.Fatal("old archive was not removed")
	}

	if err := os.WriteFile(a.cfg.authLogFile, []byte("auth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.cfg.fail2banLogFile, []byte("fail2ban\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.rotateRuntimeLogs(nowTime.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if active, _ := os.ReadFile(a.cfg.authLogFile); len(active) != 0 {
		t.Fatalf("auth log was not rotated: %q", active)
	}
	if active, _ := os.ReadFile(a.cfg.fail2banLogFile); len(active) != 0 {
		t.Fatalf("fail2ban log was not rotated: %q", active)
	}

	if err := os.WriteFile(a.cfg.bbsLogFile, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	offset, err := copyLogAppend(a.cfg.bbsLogFile, 0)
	if err != nil || offset != 3 {
		t.Fatalf("copyLogAppend offset=%d err=%v", offset, err)
	}
	offset, err = copyLogAppend(a.cfg.bbsLogFile, 3)
	if err != nil || offset != 3 {
		t.Fatalf("copyLogAppend no-op offset=%d err=%v", offset, err)
	}
	if err := os.WriteFile(a.cfg.bbsLogFile, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	offset, err = copyLogAppend(a.cfg.bbsLogFile, 3)
	if err != nil || offset != 1 {
		t.Fatalf("copyLogAppend truncated offset=%d err=%v", offset, err)
	}
}
