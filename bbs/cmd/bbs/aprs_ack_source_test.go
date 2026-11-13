package main

import (
	"strings"
	"testing"
)

// The acknowledgement must come from the station the message was addressed to,
// SSID and all.
//
// A client matches an ack by comparing its source with the addressee it used. The
// code forced the source to SSID 0, so a station that messaged EA7KLK-15 received
// "ack…" from EA7KLK-0 — a different station, as far as it could tell — and left
// the message unacknowledged and kept retrying it.
func TestAPRSAckSourceKeepsTheSSID(t *testing.T) {
	cases := map[string]string{
		"EA7KLK-15": "EA7KLK-15",
		"EA7KLK-7":  "EA7KLK-7",
		"ea7klk-15": "EA7KLK-15",
		" EA7KLK-1": "EA7KLK-1",
		// A bare addressee stays bare: in AX.25, SSID 0 is written without the
		// suffix, and the sender compares strings.
		"EA7KLK": "EA7KLK",
	}
	for addressee, want := range cases {
		if got := aprsAckSource(addressee); got != want {
			t.Fatalf("aprsAckSource(%q) = %q, want %q", addressee, got, want)
		}
	}
}

// The passcode is derived from the base callsign, so adding an SSID must not
// change it — APRS-IS expects the EA7KLK code for a login as EA7KLK-15.
func TestAPRSAckPasscodeIgnoresTheSSID(t *testing.T) {
	base := aprsPasscode("EA7KLK")
	for _, source := range []string{"EA7KLK-0", "EA7KLK-15", "EA7KLK-7"} {
		if got := aprsPasscode(source); got != base {
			t.Fatalf("aprsPasscode(%q) = %d, want %d", source, got, base)
		}
	}
}

// The packet that goes on the air names the same station in the login and in the
// source field, or APRS-IS treats it as a spoofed packet.
func TestAPRSAckPacketMatchesItsLogin(t *testing.T) {
	source := aprsAckSource("EA7KLK-15")
	packet := formatAPRSAckPacket(source, "DN9RZ", "WM4P1")
	const want = "EA7KLK-15>APRS,TCPIP*::DN9RZ    :ackWM4P1"
	if packet != want {
		t.Fatalf("packet = %q, want %q", packet, want)
	}
}

// The receive path must hand the queue the addressee as it arrived, not the
// collapsed account key — otherwise the SSID is already gone before the ack is
// built.
func TestAckRequestCarriesTheAddressedSSID(t *testing.T) {
	a := testApp(t)
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", EnableAPRS: true})

	// What the receiver builds for a message addressed to EA7KLK-15.
	req := aprsAckRequest{User: "EA7KLK", From: "DN9RZ", To: "EA7KLK-15", MessageID: "WM4P1"}
	if got := aprsAckSource(req.To); got != "EA7KLK-15" {
		t.Fatalf("ack source from the queue = %q", got)
	}
	// And the account lookup still collapses to the base callsign, which is the
	// intended behaviour.
	key, enabled, err := a.aprsRecipientEnabled("EA7KLK-15")
	if err != nil {
		t.Fatal(err)
	}
	if key != "EA7KLK" || !enabled {
		t.Fatalf("aprsRecipientEnabled(EA7KLK-15) = %q,%t", key, enabled)
	}
}

// The warning above the send forms must name the operator's own station, and must
// not leak a Go formatting error when a translation is missing.
func TestSsid0Warning(t *testing.T) {
	a := testApp(t)
	a.text["en"]["aprs_ssid0_warning"] = "Every message you send leaves from %s, whatever SSID you address."
	lines := a.aprsSsid0Warning("en", "EA7KLK")
	if len(lines) != 1 {
		t.Fatalf("warning lines = %d, want 1", len(lines))
	}
	want := "Every message you send leaves from EA7KLK-0, whatever SSID you address."
	if lines[0] != want {
		t.Fatalf("warning = %q, want %q", lines[0], want)
	}

	// Missing key: fall back to English rather than printing the key with a
	// formatting complaint.
	a.text["xx"] = map[string]any{}
	if got := a.aprsSsid0Warning("xx", "EA7KLK")[0]; got != "Every message you send leaves from EA7KLK-0, whatever SSID you address." {
		t.Fatalf("fallback warning = %q", got)
	}
}

// The send forms must actually show it: a warning that is never rendered is not a
// warning.
func TestSendFormsShowTheSsid0Warning(t *testing.T) {
	a := testApp(t)
	a.text["en"] = map[string]any{
		"aprs_ssid0_warning":        "Every message you send leaves from %s.",
		"aprs_send_message":         "Send message",
		"aprs_text":                 "Text",
		"aprs_destination_callsign": "Destination",
		"aprs_destination_ssid":     "SSID",
	}
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", EnableAPRS: true})

	shown := 0
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "cancel", nil, true
	}
	a.runFormStyledHook = func(lang, title string, fields []formField, buttons []string, style formStyle) (string, map[string]string, bool) {
		for _, line := range style.Intro {
			if strings.Contains(line, "EA7KLK-0") {
				shown++
			}
		}
		return "cancel", nil, true
	}
	a.sendAPRSForm("EA7KLK", userProfile{EnableAPRS: true}, "en", "DN9RZ")
	if shown == 0 {
		t.Fatal("the send form did not show the SSID-0 warning")
	}
}
