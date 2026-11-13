package main

import (
	"strings"
	"testing"
)

// APRS message sections.
//
// Bot traffic used to share the list with messages from other operators. These
// tests pin the classification and that the two lists really are separate.

// The classifier is content-first, peer-second, because the Thursday net is
// relayed by participants rather than by the group server. Every case here is a
// shape observed in the live database.
func TestAPRSScopeOfMessage(t *testing.T) {
	cases := []struct {
		name string
		peer string
		text string
		want aprsScope
	}{
		// The actual #APRSThursday conversation: relayed by participants and
		// tagged with the group. This is the traffic the first version missed.
		{"relayed by a participant", "KB3KBR-2", "N:HOTG happy aprsthursday!!!", aprsScopeThursday},
		{"relayed, another station", "HI3RRR-7", "N:HOTG Saludos desde la Republica Dominicana 73!!", aprsScopeThursday},
		{"relayed from the net bot", "APRSPH", "N:HOTG net message", aprsScopeThursday},

		// The group server itself, for Thursday.
		{"ansrvr summary", "ANSRVR", "N:HOTG 24 Messages Sent", aprsScopeThursday},
		{"ansrvr removal", "ANSRVR", "N:HOTG Removed From Group{GJ}", aprsScopeThursday},

		// Other groups on the same server are not Thursday.
		{"other group", "ANSRVR", "N:AARN Check in", aprsScopeGroups},
		{"other group removed", "ANSRVR", "N:NEAPRS Removed From Group{UT}", aprsScopeGroups},
		{"other group relayed", "TI2BSH-15", "N:NEAPRS hello", aprsScopeGroups},

		// An acknowledgement names no group, so it stays with the group server
		// rather than being guessed into a net it may not belong to.
		{"acknowledgement", "ANSRVR", "ack1B}", aprsScopeGroups},
		{"monitoring notice", "ANSRVR", "Now Monitoring HOTG{QW}", aprsScopeThursday},

		// Our own outgoing commands.
		{"join command", "ANSRVR", "CQ HOTG", aprsScopeThursday},
		{"keepalive", "ANSRVR", "K HOTG", aprsScopeThursday},
		{"leave", "ANSRVR", "U HOTG", aprsScopeThursday},

		// The APRSPH bot, by peer.
		{"aprsph reply", "APRSPH", "1113 0501Z:QSP 8 stations. LIST for recipients.", aprsScopeAPRSPH},
		{"aprsph checkin ack", "APRSPH", "Ur checked in 1113 0204Z. QRX pls til 2359Z.", aprsScopeAPRSPH},
		{"aprsph numbered line", "APRSPH", "7.11-10 2306Z NB9D-9:hello", aprsScopeAPRSPH},

		// Direct traffic, including text that merely looks like a tag.
		{"person", "DN9RZ", "hello", aprsScopeDirect},
		{"person with ssid", "DN9RZ-10", "hi", aprsScopeDirect},
		{"N: but no group", "DN9RZ", "N: hello", aprsScopeDirect},
		{"N: with too long a tag", "DN9RZ", "N:ABCDEFGHI text", aprsScopeDirect},
		{"hotg in ordinary prose", "DN9RZ", "join us on hotg tomorrow", aprsScopeDirect},
	}
	for _, tc := range cases {
		if got := aprsScopeOfMessage(tc.peer, tc.text); got != tc.want {
			t.Fatalf("%s: aprsScopeOfMessage(%q, %q) = %q, want %q",
				tc.name, tc.peer, tc.text, got, tc.want)
		}
	}
}

func TestAPRSGroupTag(t *testing.T) {
	cases := map[string]struct {
		group string
		ok    bool
	}{
		"N:HOTG 24 Messages Sent": {"HOTG", true},
		"N:HOTG":                  {"HOTG", true},
		"N:AARN hello":            {"AARN", true},
		"N:HOTG{a}":               {"HOTG", true},
		"n:hotg lower":            {"HOTG", true},
		"N:":                      {"", false},
		"N:AB text":               {"AB", true},
		"N:NEAPRS hello":          {"NEAPRS", true},
		"N:ABCDEFGH text":         {"ABCDEFGH", true},
		"N:ABCDEFGHI text":        {"", false},
		"ack1B}":                  {"", false},
		"hello":                   {"", false},
	}
	for text, want := range cases {
		group, ok := aprsGroupTag(text)
		if ok != want.ok || group != want.group {
			t.Fatalf("aprsGroupTag(%q) = %q,%t; want %q,%t", text, group, ok, want.group, want.ok)
		}
	}
}

func TestFilterSentByScope(t *testing.T) {
	rows := []sentAPRS{
		{To: "DN9RZ", Text: "hello"},
		{To: "APRSPH", Text: "LIST"},
		{To: "ANSRVR", Text: "CQ HOTG net"},
		{To: "EA1ABC-5", Text: "hi"},
		{To: "APRSPH", Text: "?APRSP"},
	}
	if got := len(filterSentByScope(rows, aprsScopeDirect)); got != 2 {
		t.Fatalf("direct section = %d messages, want 2", got)
	}
	if got := len(filterSentByScope(rows, aprsScopeAPRSPH)); got != 2 {
		t.Fatalf("APRSPH section = %d messages, want 2", got)
	}
	if got := len(filterSentByScope(rows, aprsScopeThursday)); got != 1 {
		t.Fatalf("Thursday section = %d messages, want 1", got)
	}
	// Every message lands in exactly one section.
	total := 0
	for _, scope := range aprsScopes {
		total += len(filterSentByScope(rows, scope))
	}
	if total != len(rows) {
		t.Fatalf("the sections hold %d of %d messages", total, len(rows))
	}
}

func TestFilterReceivedByScope(t *testing.T) {
	rows := []receivedAPRS{
		{From: "DN9RZ", Text: "hi"},
		{From: "APRSPH", Text: "1113 0501Z:QSP 8 stations"},
		{From: "ANSRVR", Text: "N:HOTG 239 Messages Sent"},
		{From: "ANSRVR", Text: "ack1B}"},                  // an ack names no group
		{From: "KB3KBR-2", Text: "N:HOTG happy thursday"}, // relayed by a station
		{From: "ANSRVR", Text: "N:AARN Check in"},         // a different group
	}
	if got := len(filterReceivedByScope(rows, aprsScopeDirect)); got != 1 {
		t.Fatalf("direct section = %d, want 1", got)
	}
	if got := len(filterReceivedByScope(rows, aprsScopeAPRSPH)); got != 1 {
		t.Fatalf("APRSPH section = %d, want 1", got)
	}
	// Thursday is decided by the tag, so it holds the relayed message too.
	if got := len(filterReceivedByScope(rows, aprsScopeThursday)); got != 2 {
		t.Fatalf("Thursday section = %d, want 2", got)
	}
	// The ack and the other group stay with the group server.
	if got := len(filterReceivedByScope(rows, aprsScopeGroups)); got != 2 {
		t.Fatalf("groups section = %d, want 2", got)
	}
	// A body that happens to carry an "N:APRSPH" tag is group traffic, not bot
	// traffic: the tag decides before the peer does.
	if got := aprsScopeOfMessage("APRSPH", "N:APRSPH 3 Messages Sent"); got != aprsScopeGroups {
		t.Fatalf("a tagged body from APRSPH classified as %q", got)
	}
}

// The section menu counts what each section holds, so an empty one is visibly
// empty rather than a surprise after a click.
func TestSectionCounts(t *testing.T) {
	received := []receivedAPRS{
		{From: "DN9RZ", Text: "hi"},
		{From: "APRSPH", Text: "checkin ack"},
		{From: "APRSPH", Text: "another"},
		{From: "ANSRVR", Text: "N:HOTG 24 Messages Sent"},
		{From: "ANSRVR", Text: "ack1B}"},
	}
	counts := countReceivedScopes(received)
	if counts[aprsScopeDirect] != 1 || counts[aprsScopeAPRSPH] != 2 ||
		counts[aprsScopeThursday] != 1 || counts[aprsScopeGroups] != 1 {
		t.Fatalf("received counts = %#v", counts)
	}
	sent := []sentAPRS{
		{To: "ANSRVR", Text: "CQ HOTG"},
		{To: "EA1ABC", Text: "hi"},
		{To: "APRSPH", Text: "LIST"},
	}
	counts = countSentScopes(sent)
	if counts[aprsScopeDirect] != 1 || counts[aprsScopeAPRSPH] != 1 ||
		counts[aprsScopeThursday] != 1 || counts[aprsScopeGroups] != 0 {
		t.Fatalf("sent counts = %#v", counts)
	}
}

// The list a section opens must contain only that section's messages.
func TestReceivedSectionShowsOnlyItsOwnMessages(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	for _, msg := range []receivedAPRS{
		{From: "DN9RZ", To: "EA7KLK", Text: "from a person"},
		{From: "APRSPH", To: "EA7KLK", Text: "from the net bot"},
		{From: "ANSRVR", To: "EA7KLK", Text: "N:HOTG 24 Messages Sent"},
		{From: "KB3KBR-2", To: "EA7KLK", Text: "N:HOTG happy thursday"},
	} {
		if _, err := a.addReceivedRecord("EA7KLK", msg); err != nil {
			t.Fatal(err)
		}
	}

	labels := map[aprsScope][]string{}
	for _, scope := range aprsScopes {
		var captured []string
		a.runMenuHook = func(lang, title, header string, opts []option) string {
			captured = nil
			for _, opt := range opts {
				if opt.value != "q" {
					captured = append(captured, opt.label)
				}
			}
			return "q"
		}
		a.showReceivedAPRS("EA7KLK", userProfile{EnableAPRS: true}, "en", scope)
		labels[scope] = captured
	}

	if len(labels[aprsScopeDirect]) != 1 {
		t.Fatalf("direct section shows %d rows, want 1", len(labels[aprsScopeDirect]))
	}
	if len(labels[aprsScopeAPRSPH]) != 1 {
		t.Fatalf("APRSPH section shows %d rows, want 1", len(labels[aprsScopeAPRSPH]))
	}
	// Two: the server's summary and the message a participant relayed.
	if len(labels[aprsScopeThursday]) != 2 {
		t.Fatalf("Thursday section shows %d rows, want 2", len(labels[aprsScopeThursday]))
	}
	if len(labels[aprsScopeGroups]) != 0 {
		t.Fatalf("groups section shows %d rows, want 0", len(labels[aprsScopeGroups]))
	}
	// The person's message must not be in a bot section, and vice versa.
	if !strings.Contains(strings.Join(labels[aprsScopeDirect], " "), "DN9RZ") {
		t.Fatalf("the direct section does not show the message from DN9RZ: %#v", labels[aprsScopeDirect])
	}
	if strings.Contains(strings.Join(labels[aprsScopeAPRSPH], " "), "DN9RZ") {
		t.Fatal("the APRSPH section shows a message from another station")
	}
}

// Backing out of the section menu must not open a list.
func TestReceivedSectionMenuCanBeCancelled(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	opened := false
	a.runMenuHook = func(lang, title, header string, opts []option) string { return "q" }
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	a.aprsReceivedSection("EA7KLK", userProfile{EnableAPRS: true}, "en")
	if opened {
		t.Fatal("cancelling the section menu opened a list")
	}
}

func TestParseMenuIndex(t *testing.T) {
	if idx, err := parseMenuIndex("2", 3); err != nil || idx != 1 {
		t.Fatalf("parseMenuIndex(2,3) = %d,%v", idx, err)
	}
	for _, bad := range []string{"q", "", "0", "4", "abc"} {
		if _, err := parseMenuIndex(bad, 3); err == nil {
			t.Fatalf("parseMenuIndex(%q,3) accepted the choice", bad)
		}
	}
}
