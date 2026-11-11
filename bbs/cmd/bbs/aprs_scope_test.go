package main

import (
	"strings"
	"testing"
)

// APRS message sections.
//
// Bot traffic used to share the list with messages from other operators. These
// tests pin the classification and that the two lists really are separate.

func TestAPRSScopeOfPeer(t *testing.T) {
	cases := map[string]aprsScope{
		"APRSPH":   aprsScopeAPRSPH,
		"aprsph":   aprsScopeAPRSPH,
		" APRSPH ": aprsScopeAPRSPH,
		"APRSPH-0": aprsScopeAPRSPH,
		"ANSRVR":   aprsScopeThursday,
		"ansrvr":   aprsScopeThursday,
		"DN9RZ":    aprsScopeDirect,
		"DN9RZ-10": aprsScopeDirect,
		"EA7KLK":   aprsScopeDirect,
		"":         aprsScopeDirect,
		"APRSPHX":  aprsScopeDirect, // a different station whose callsign starts the same
	}
	for peer, want := range cases {
		if got := aprsScopeOfPeer(peer); got != want {
			t.Fatalf("aprsScopeOfPeer(%q) = %q, want %q", peer, got, want)
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
		{From: "APRSPH", Text: "N:APRSPH 3 Messages Sent"},
		{From: "ANSRVR", Text: "N:HOTG 239 Messages Sent"},
		{From: "ANSRVR", Text: "ack1B}"}, // an ack carries no group name
	}
	if got := len(filterReceivedByScope(rows, aprsScopeDirect)); got != 1 {
		t.Fatalf("direct section = %d, want 1", got)
	}
	if got := len(filterReceivedByScope(rows, aprsScopeAPRSPH)); got != 1 {
		t.Fatalf("APRSPH section = %d, want 1", got)
	}
	// Both ANSRVR rows land together, including the ack that names no group —
	// that is why the split is by peer and not by content.
	if got := len(filterReceivedByScope(rows, aprsScopeThursday)); got != 2 {
		t.Fatalf("Thursday section = %d, want 2", got)
	}
}

// The section menu counts what each section holds, so an empty one is visibly
// empty rather than a surprise after a click.
func TestSectionCounts(t *testing.T) {
	received := []receivedAPRS{{From: "DN9RZ"}, {From: "APRSPH"}, {From: "ANSRVR"}, {From: "APRSPH"}}
	counts := countReceivedScopes(received)
	if counts[aprsScopeDirect] != 1 || counts[aprsScopeAPRSPH] != 2 || counts[aprsScopeThursday] != 1 {
		t.Fatalf("received counts = %#v", counts)
	}
	sent := []sentAPRS{{To: "ANSRVR"}, {To: "EA1ABC"}}
	counts = countSentScopes(sent)
	if counts[aprsScopeDirect] != 1 || counts[aprsScopeAPRSPH] != 0 || counts[aprsScopeThursday] != 1 {
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
		{From: "ANSRVR", To: "EA7KLK", Text: "from the group server"},
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
	if len(labels[aprsScopeThursday]) != 1 {
		t.Fatalf("Thursday section shows %d rows, want 1", len(labels[aprsScopeThursday]))
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
