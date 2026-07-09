package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	db, err := openDatabase(filepath.Join(dir, "bbs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return &app{cfg: config{dataDir: dir, dbFile: filepath.Join(dir, "bbs.sqlite")}, db: db, text: map[string]map[string]any{"en": {}}}
}

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

func TestFormatAPRSMessagePacket(t *testing.T) {
	got := formatAPRSMessagePacket("ea7klk-0", "ea1abc", "Hello")
	want := "EA7KLK-0>APRS,TCPIP*::EA1ABC   :Hello"
	if got != want {
		t.Fatalf("formatAPRSMessagePacket() = %q, want %q", got, want)
	}
	got = formatAPRSMessagePacket("ea7klk-0", "ea1abc-0", withAPRSMessageID("Hello", "a1"))
	want = "EA7KLK-0>APRS,TCPIP*::EA1ABC-0 :Hello{A1"
	if got != want {
		t.Fatalf("formatAPRSMessagePacket(with id) = %q, want %q", got, want)
	}
	if got, want := formatAPRSAckPacket("ea7klk-0", "ea1abc-0", "a1"), "EA7KLK-0>APRS,TCPIP*::EA1ABC-0 :ackA1"; got != want {
		t.Fatalf("formatAPRSAckPacket() = %q, want %q", got, want)
	}
}

func TestComposeAPRSDestinationBlankSSID(t *testing.T) {
	if got, want := composeAPRSDestination("ea1abc", "0"), "EA1ABC-0"; got != want {
		t.Fatalf("composeAPRSDestination(default ssid) = %q, want %q", got, want)
	}
	if got, want := composeAPRSDestination("ea1abc", ""), "EA1ABC"; got != want {
		t.Fatalf("composeAPRSDestination(blank ssid) = %q, want %q", got, want)
	}
	if call, ssid := splitAPRSDestination("ANSRVR"); call != "ANSRVR" || ssid != "" {
		t.Fatalf("splitAPRSDestination(ANSRVR) = %q/%q, want ANSRVR/blank", call, ssid)
	}
}

func TestParseAPRSMessageLine(t *testing.T) {
	msg, ok := parseAPRSMessageLine("EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :Hello from APRS{42")
	if !ok {
		t.Fatal("parseAPRSMessageLine() did not recognize a message packet")
	}
	if msg.From != "EA1ABC-7" || msg.To != "EA7KLK-0" || msg.Text != "Hello from APRS" || msg.MessageID != "42" || !strings.Contains(msg.Raw, "{42") {
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

func TestParseAPRSMessageLineRejectsAckAndRej(t *testing.T) {
	cases := []string{
		"EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :ack42",
		"EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :rej42",
		"EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :ack42{99",
	}
	for _, raw := range cases {
		if _, ok := parseAPRSMessageLine(raw); ok {
			t.Fatalf("parseAPRSMessageLine() accepted APRS response packet %q", raw)
		}
		packet, ok := parseAPRSMessagePacket(raw)
		if !ok || packet.AckID == "" && packet.RejID == "" {
			t.Fatalf("parseAPRSMessagePacket() did not expose APRS response packet %q: %#v ok=%v", raw, packet, ok)
		}
	}
	msg, ok := parseAPRSMessageLine("EA1ABC-7>APRS,TCPIP*,qAC,T2TEST::EA7KLK-0 :acknowledged and copied")
	if !ok || msg.Text != "acknowledged and copied" {
		t.Fatalf("parseAPRSMessageLine() rejected a normal text message: %#v ok=%v", msg, ok)
	}
}

func TestParseAPRSMultipartPrefix(t *testing.T) {
	part, total, body, ok := parseAPRSMultipartPrefix("[2/3] middle text")
	if !ok || part != 2 || total != 3 || body != "middle text" {
		t.Fatalf("parseAPRSMultipartPrefix() = part=%d total=%d body=%q ok=%v", part, total, body, ok)
	}
	if _, _, _, ok := parseAPRSMultipartPrefix("not multipart"); ok {
		t.Fatal("parseAPRSMultipartPrefix() accepted a normal message")
	}
}

func TestCollectAPRSMessagePartReassemblesOutOfOrder(t *testing.T) {
	buffers := map[string]*aprsMultipartBuffer{}
	second := receivedAPRS{At: "2026-07-08 10:00 UTC", From: "EA1ABC-7", To: "EA7KLK-0", Text: "[2/3] two ", Raw: "raw2"}
	first := receivedAPRS{At: "2026-07-08 09:59 UTC", From: "EA1ABC-7", To: "EA7KLK-0", Text: "[1/3] one ", Raw: "raw1"}
	third := receivedAPRS{At: "2026-07-08 10:01 UTC", From: "EA1ABC-7", To: "EA7KLK-0", Text: "[3/3] three", Raw: "raw3"}

	if ready, waiting := collectAPRSMessagePart(buffers, second); !waiting || len(ready) != 0 {
		t.Fatalf("second part ready=%d waiting=%v, want waiting", len(ready), waiting)
	}
	if ready, waiting := collectAPRSMessagePart(buffers, first); !waiting || len(ready) != 0 {
		t.Fatalf("first part ready=%d waiting=%v, want waiting", len(ready), waiting)
	}
	ready, waiting := collectAPRSMessagePart(buffers, third)
	if waiting || len(ready) != 1 {
		t.Fatalf("third part ready=%d waiting=%v, want one complete message", len(ready), waiting)
	}
	got := ready[0]
	if got.Text != "one two three" {
		t.Fatalf("reassembled text = %q, want %q", got.Text, "one two three")
	}
	if got.Raw != "raw1\nraw2\nraw3" {
		t.Fatalf("reassembled raw = %q", got.Raw)
	}
	if len(buffers) != 0 {
		t.Fatalf("buffers still has %d entries, want 0", len(buffers))
	}
}

func TestStoreReceivedAPRSOnlyEnabledUsers(t *testing.T) {
	a := testApp(t)
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
	history := a.loadReceivedHistory("EA7KLK")
	if got := len(history); got != 1 {
		t.Fatalf("stored enabled message count = %d, want 1", got)
	}
	if got := history[0].Text; got != "Enabled" {
		t.Fatalf("stored enabled message text = %q, want %q", got, "Enabled")
	}
	if got := len(a.loadReceivedHistory("EA1OFF")); got != 0 {
		t.Fatalf("stored disabled message count = %d, want 0", got)
	}
}

func TestStoreReceivedAPRSRejectsAckAndRej(t *testing.T) {
	a := testApp(t)
	if err := a.saveUsers(map[string]userProfile{"EA7KLK": {EnableAPRS: true}}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"ack42", "rej42"} {
		_, saved, err := a.storeReceivedAPRS(receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: text, Raw: text})
		if err != nil || saved {
			t.Fatalf("storeReceivedAPRS(%q) saved=%v err=%v, want not saved", text, saved, err)
		}
	}
	if got := len(a.loadReceivedHistory("EA7KLK")); got != 0 {
		t.Fatalf("stored APRS response message count = %d, want 0", got)
	}
}

func TestSentAPRSHistoryLimitAndDelete(t *testing.T) {
	a := testApp(t)
	for i := 0; i < sentHistoryLimit+2; i++ {
		_, err := a.addSentRecord("EA7KLK", sentAPRS{At: now(), From: "EA7KLK-0", To: "EA1ABC", Text: "message", Status: aprsStatusSent})
		if err != nil {
			t.Fatal(err)
		}
	}
	history := a.loadSentHistory("EA7KLK")
	if got := len(history); got != sentHistoryLimit {
		t.Fatalf("sent history count = %d, want %d", got, sentHistoryLimit)
	}
	if history[0].ID == 0 {
		t.Fatal("loaded sent history row has no database ID")
	}
	if err := a.deleteSentRecord(history[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(a.loadSentHistory("EA7KLK")); got != sentHistoryLimit-1 {
		t.Fatalf("sent history count after delete = %d, want %d", got, sentHistoryLimit-1)
	}
}

func TestSentAPRSAckFlow(t *testing.T) {
	a := testApp(t)
	_, err := a.addSentRecord("EA7KLK", sentAPRS{
		At:     now(),
		From:   "EA7KLK-0",
		To:     "EA1ABC-0",
		Text:   "hello",
		Status: aprsStatusSent,
		Parts: []sentAPRSPart{
			{Number: 1, Text: "[1/2] hello ", Status: aprsStatusSent, MessageID: "ID001"},
			{Number: 2, Text: "[2/2] world", Status: aprsStatusSent, MessageID: "ID002"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	history := a.loadSentHistory("EA7KLK")
	if got := sentAckIcon(history[0]); got != "?" {
		t.Fatalf("initial multipart ack icon = %q, want ?", got)
	}
	matched, err := a.markSentAPRSAck("EA1ABC-0", "EA7KLK-0", "ID001")
	if err != nil || !matched {
		t.Fatalf("first ack matched=%v err=%v, want matched", matched, err)
	}
	history = a.loadSentHistory("EA7KLK")
	if history[0].Acked || !history[0].Parts[0].Acked || history[0].Parts[1].Acked {
		t.Fatalf("unexpected partial ack state: %#v", history[0])
	}
	if got := sentAckIcon(history[0]); got != "?" {
		t.Fatalf("partial multipart ack icon = %q, want ?", got)
	}
	matched, err = a.markSentAPRSAck("EA1ABC-0", "EA7KLK-0", "ID002")
	if err != nil || !matched {
		t.Fatalf("second ack matched=%v err=%v, want matched", matched, err)
	}
	history = a.loadSentHistory("EA7KLK")
	if !history[0].Acked || !history[0].Parts[0].Acked || !history[0].Parts[1].Acked {
		t.Fatalf("unexpected complete ack state: %#v", history[0])
	}
	if got := sentAckIcon(history[0]); got != "✓" {
		t.Fatalf("complete multipart ack icon = %q, want checkmark", got)
	}
}

func TestAPRSDetailRowsDoNotSplitMultipartText(t *testing.T) {
	a := testApp(t)
	sent := sentAPRS{
		At:     now(),
		From:   "EA7KLK-0",
		To:     "EA1ABC",
		Text:   "one two\nthree",
		Status: aprsStatusSent,
		Parts: []sentAPRSPart{
			{Number: 1, Text: "[1/2] one two ", Status: aprsStatusSent},
			{Number: 2, Text: "[2/2] three", Status: aprsStatusSent},
		},
	}
	for _, row := range a.aprsSentDetailRows("en", sent) {
		if strings.Contains(row[1], "[1/2]") || strings.Contains(row[1], "[2/2]") {
			t.Fatalf("sent detail leaked multipart chunk text: %#v", row)
		}
	}
	received := receivedAPRS{At: now(), From: "EA1ABC", To: "EA7KLK-0", Text: "one two\nthree", Raw: "raw1\nraw2"}
	for _, row := range a.aprsReceivedDetailRows("en", received) {
		if len(row) > 1 && strings.Contains(row[1], "\n") {
			t.Fatalf("received detail contains embedded newline: %#v", row)
		}
	}
	rows := a.aprsReceivedDetailRows("en", received)
	if rows[3][1] != "one two three" {
		t.Fatalf("received detail text = %q, want %q", rows[3][1], "one two three")
	}
	if len(rows) < 8 || rows[4][0] != "" || !strings.HasPrefix(rows[5][0], "---") || rows[6][0] != "" {
		t.Fatalf("received detail missing separator before raw packet: %#v", rows)
	}
}

func TestAPRSHistoryLoadsNormalizeMultipartNewlines(t *testing.T) {
	a := testApp(t)
	if _, err := a.addSentRecord("EA7KLK", sentAPRS{
		At:     now(),
		From:   "EA7KLK-0",
		To:     "EA1ABC",
		Text:   "sent one\nsent two",
		Status: aprsStatusSent,
		Parts: []sentAPRSPart{
			{Number: 1, Text: "[1/2] sent one\n", Status: aprsStatusSent, Detail: "ok\npart one"},
			{Number: 2, Text: "[2/2] sent two", Status: aprsStatusSent},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addReceivedRecord("EA7KLK", receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: "received one\nreceived two", Raw: "raw1\nraw2"}); err != nil {
		t.Fatal(err)
	}
	sentHistory := a.loadSentHistory("EA7KLK")
	if got := sentHistory[0].Text; got != "sent one sent two" {
		t.Fatalf("loaded sent text = %q, want %q", got, "sent one sent two")
	}
	if got := sentHistory[0].Parts[0].Detail; got != "ok part one" {
		t.Fatalf("loaded sent part detail = %q, want %q", got, "ok part one")
	}
	receivedHistory := a.loadReceivedHistory("EA7KLK")
	if got := receivedHistory[0].Text; got != "received one received two" {
		t.Fatalf("loaded received text = %q, want %q", got, "received one received two")
	}
	if rows := a.aprsSentDetailRows("en", sentHistory[0]); strings.Contains(rows[5][1], "\n") {
		t.Fatalf("sent detail text contains newline: %#v", rows[5])
	}
	if rows := a.aprsReceivedDetailRows("en", receivedHistory[0]); strings.Contains(rows[3][1], "\n") {
		t.Fatalf("received detail text contains newline: %#v", rows[3])
	}
}

func TestReceivedAPRSHistoryLimitAndDelete(t *testing.T) {
	a := testApp(t)
	for i := 0; i < receivedHistoryLimit+2; i++ {
		_, err := a.addReceivedRecord("EA7KLK", receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: "message", Raw: fmt.Sprintf("raw %d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	history := a.loadReceivedHistory("EA7KLK")
	if got := len(history); got != receivedHistoryLimit {
		t.Fatalf("received history count = %d, want %d", got, receivedHistoryLimit)
	}
	if history[0].ID == 0 {
		t.Fatal("loaded received history row has no database ID")
	}
	if err := a.deleteReceivedRecord(history[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := len(a.loadReceivedHistory("EA7KLK")); got != receivedHistoryLimit-1 {
		t.Fatalf("received history count after delete = %d, want %d", got, receivedHistoryLimit-1)
	}
}

func TestNormalizeReceivedAPRSStore(t *testing.T) {
	a := testApp(t)
	if err := a.saveUsers(map[string]userProfile{"EA7KLK": {EnableAPRS: true}}); err != nil {
		t.Fatal(err)
	}
	_, err := a.addReceivedRecord("EA7KLK", receivedAPRS{
		At:   now(),
		From: "EA1ABC-7",
		To:   "EA7KLK-0",
		Text: "Old text{2044",
		Raw:  "EA1ABC-7>APRS::EA7KLK-0 :Old text{2044",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.Model(&dbAPRSReceived{}).Where("user_callsign = ?", "EA7KLK").Update("text", "Old text{2044").Error; err != nil {
		t.Fatal(err)
	}
	if err := a.normalizeReceivedAPRSStore(); err != nil {
		t.Fatal(err)
	}
	got := a.loadReceivedHistory("EA7KLK")
	if text := got[0].Text; text != "Old text" {
		t.Fatalf("normalized text = %q, want %q", text, "Old text")
	}
	if raw := got[0].Raw; !strings.Contains(raw, "{2044") {
		t.Fatalf("normalized raw did not preserve message ID: %q", raw)
	}
}

func TestAPRSReceiverLoginUsesConfiguredSysop(t *testing.T) {
	a := &app{cfg: config{sysops: map[string]bool{"EA7KLK": true}}}
	if got, want := a.aprsReceiverLogin(), "EA7KLK"; got != want {
		t.Fatalf("aprsReceiverLogin() = %q, want %q", got, want)
	}
}
