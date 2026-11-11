package main

import "testing"

// The APRS receive path must read one row, not the table.
//
// A full scan here capped the receiver at about six packets a second at ten
// thousand users. Behaviour is what can be asserted directly: the three lookups
// agree with a hand-built table.
func TestAPRSRecipientLookupsTargetOneAccount(t *testing.T) {
	a := testApp(t)
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", EnableAPRS: true})
	a.saveUser("EA1ABC", userProfile{FullName: "Ana", Language: "en", EnableAPRS: false})
	a.saveUser("DN9RZ", userProfile{FullName: "Gone", Language: "en", EnableAPRS: true, Disabled: true})

	cases := []struct {
		to      string
		wantKey string
		exists  bool
		enabled bool
	}{
		{"EA7KLK", "EA7KLK", true, true},
		{"EA7KLK-7", "EA7KLK", true, true}, // an SSID resolves to the account
		{"ea7klk", "EA7KLK", true, true},   // callsigns are normalised
		{"EA1ABC", "EA1ABC", true, false},  // exists but APRS off
		{"DN9RZ", "DN9RZ", true, false},    // disabled account cannot receive
		{"N0CALL", "N0CALL", false, false}, // unknown
	}
	for _, tc := range cases {
		key, exists, err := a.aprsRecipientExists(tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.to, err)
		}
		if key != tc.wantKey || exists != tc.exists {
			t.Fatalf("aprsRecipientExists(%q) = %q,%t; want %q,%t", tc.to, key, exists, tc.wantKey, tc.exists)
		}
		key, enabled, err := a.aprsRecipientEnabled(tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.to, err)
		}
		if key != tc.wantKey || enabled != tc.enabled {
			t.Fatalf("aprsRecipientEnabled(%q) = %q,%t; want %q,%t", tc.to, key, enabled, tc.wantKey, tc.enabled)
		}
	}
}

// storeReceivedAPRS is the per-packet path; it must store for an enabled account
// and drop the packet quietly for a disabled or APRS-off one.
func TestStoreReceivedAPRSUsesTheTargetedLookup(t *testing.T) {
	a := testApp(t)
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", EnableAPRS: true})
	a.saveUser("EA1ABC", userProfile{FullName: "Ana", Language: "en", EnableAPRS: false})

	msg := receivedAPRS{From: "DN9RZ", To: "EA7KLK", Text: "hello", At: now()}
	key, saved, err := a.storeReceivedAPRS(msg)
	if err != nil {
		t.Fatal(err)
	}
	if key != "EA7KLK" || !saved {
		t.Fatalf("storeReceivedAPRS for an enabled account = %q,%t", key, saved)
	}

	_, saved, err = a.storeReceivedAPRS(receivedAPRS{From: "DN9RZ", To: "EA1ABC", Text: "hello", At: now()})
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Fatal("storeReceivedAPRS stored a packet for an account with APRS off")
	}

	var total int64
	a.db.Model(&dbAPRSReceived{}).Count(&total)
	if total != 1 {
		t.Fatalf("stored %d received rows, want 1", total)
	}
}
