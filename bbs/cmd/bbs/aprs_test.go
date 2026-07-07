package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAPRSPasscode(t *testing.T) {
	if got, want := aprsPasscode("EA7KLK-0"), 19875; got != want {
		t.Fatalf("aprsPasscode() = %d, want %d", got, want)
	}
}

func TestSplitAPRSMessage(t *testing.T) {
	text := strings.Repeat("A", aprsMessageLimit*2+10)
	parts := splitAPRSMessage(text)
	if len(parts) < 3 {
		t.Fatalf("splitAPRSMessage() returned %d parts, want at least 3", len(parts))
	}
	for _, part := range parts {
		if got := len([]rune(part)); got > aprsMessageLimit {
			t.Fatalf("part length = %d, want <= %d: %q", got, aprsMessageLimit, part)
		}
	}
	if !strings.HasPrefix(parts[0], "[1/") {
		t.Fatalf("first split part is missing numbering prefix: %q", parts[0])
	}
}

func TestParseAPRSMessageLine(t *testing.T) {
	msg, ok := parseAPRSMessageLine("EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :Hello from APRS{42")
	if !ok {
		t.Fatal("parseAPRSMessageLine() did not recognize a message packet")
	}
	if msg.From != "EA1ABC-7" || msg.To != "EA7KLK-0" || msg.Text != "Hello from APRS" || !strings.Contains(msg.Raw, "{42") {
		t.Fatalf("unexpected parsed message: %#v", msg)
	}
}

func TestStripAPRSMessageID(t *testing.T) {
	cases := map[string]string{
		"Hello{2044":       "Hello",
		"Hello world {A12": "Hello world",
		"Keep {braces}":    "Keep {braces}",
		"Plain message":    "Plain message",
	}
	for input, want := range cases {
		if got := stripAPRSMessageID(input); got != want {
			t.Fatalf("stripAPRSMessageID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseAPRSMessageLineRejectsPosition(t *testing.T) {
	if _, ok := parseAPRSMessageLine("EA1ABC>APRS,TCPIP*,qAC,T2TEST:!3759.00N/00107.00W-Test"); ok {
		t.Fatal("parseAPRSMessageLine() accepted a non-message packet")
	}
}

func TestStoreReceivedAPRSOnlyEnabledUsers(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{
		dataDir:          dir,
		usersFile:        filepath.Join(dir, "users.json"),
		aprsReceivedFile: filepath.Join(dir, "aprs", "received.json"),
	}}
	users := map[string]userProfile{
		"EA7KLK": {EnableAPRS: true},
		"EA1OFF": {EnableAPRS: false},
	}
	if err := a.saveUsers(users); err != nil {
		t.Fatal(err)
	}
	_, saved, err := a.storeReceivedAPRS(receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: "Enabled{2044", Raw: "one"})
	if err != nil || !saved {
		t.Fatalf("storeReceivedAPRS(enabled) saved=%v err=%v", saved, err)
	}
	_, saved, err = a.storeReceivedAPRS(receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA1OFF-0", Text: "Disabled", Raw: "two"})
	if err != nil || saved {
		t.Fatalf("storeReceivedAPRS(disabled) saved=%v err=%v", saved, err)
	}
	all := map[string][]receivedAPRS{}
	if err := readJSON(a.cfg.aprsReceivedFile, &all, map[string][]receivedAPRS{}); err != nil {
		t.Fatal(err)
	}
	if got := len(all["EA7KLK"]); got != 1 {
		t.Fatalf("stored enabled message count = %d, want 1", got)
	}
	if got := all["EA7KLK"][0].Text; got != "Enabled" {
		t.Fatalf("stored enabled message text = %q, want %q", got, "Enabled")
	}
	if got := len(all["EA1OFF"]); got != 0 {
		t.Fatalf("stored disabled message count = %d, want 0", got)
	}
}

func TestNormalizeReceivedAPRSStore(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{aprsReceivedFile: filepath.Join(dir, "aprs", "received.json")}}
	all := map[string][]receivedAPRS{
		"EA7KLK": {{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: "Old text{2044", Raw: "EA1ABC-7>APRS::EA7KLK-0 :Old text{2044"}},
	}
	if err := writeJSON(a.cfg.aprsReceivedFile, all); err != nil {
		t.Fatal(err)
	}
	if err := a.normalizeReceivedAPRSStore(); err != nil {
		t.Fatal(err)
	}
	got := map[string][]receivedAPRS{}
	if err := readJSON(a.cfg.aprsReceivedFile, &got, map[string][]receivedAPRS{}); err != nil {
		t.Fatal(err)
	}
	if text := got["EA7KLK"][0].Text; text != "Old text" {
		t.Fatalf("normalized text = %q, want %q", text, "Old text")
	}
	if raw := got["EA7KLK"][0].Raw; !strings.Contains(raw, "{2044") {
		t.Fatalf("normalized raw did not preserve message ID: %q", raw)
	}
}

func TestAPRSReceiverLoginUsesConfiguredSysop(t *testing.T) {
	a := &app{cfg: config{sysops: map[string]bool{"EA7KLK": true}}}
	if got, want := a.aprsReceiverLogin(), "EA7KLK"; got != want {
		t.Fatalf("aprsReceiverLogin() = %q, want %q", got, want)
	}
}
