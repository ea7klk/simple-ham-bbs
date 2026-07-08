package main

import "testing"

func TestWouldRemoveLastSysopWithProfile(t *testing.T) {
	a := &app{cfg: config{sysops: map[string]bool{"EA7KLK": true}}}
	users := map[string]userProfile{
		"EA7KLK": {IsSysop: true},
		"EA1ABC": {},
	}
	disabled := users["EA7KLK"]
	disabled.Disabled = true
	if !a.wouldRemoveLastSysopWithProfile(users, "EA7KLK", disabled) {
		t.Fatal("disabling the last active sysop was not blocked")
	}
	users["EA1ABC"] = userProfile{IsSysop: true}
	if a.wouldRemoveLastSysopWithProfile(users, "EA7KLK", disabled) {
		t.Fatal("disabling one sysop was blocked even though another active sysop exists")
	}
}

func TestDeleteUserAPRSHistory(t *testing.T) {
	a := testApp(t)
	if _, err := a.addSentRecord("EA7KLK", sentAPRS{
		At:     now(),
		From:   "EA7KLK-0",
		To:     "EA1ABC",
		Text:   "delete me",
		Status: aprsStatusSent,
		Parts:  []sentAPRSPart{{Number: 1, Text: "delete me", Status: aprsStatusSent}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addReceivedRecord("EA7KLK", receivedAPRS{At: now(), From: "EA1ABC-7", To: "EA7KLK-0", Text: "delete me", Raw: "delete raw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addSentRecord("EA1ABC", sentAPRS{At: now(), From: "EA1ABC-0", To: "EA7KLK", Text: "keep me", Status: aprsStatusSent}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.addReceivedRecord("EA1ABC", receivedAPRS{At: now(), From: "EA7KLK-0", To: "EA1ABC-0", Text: "keep me", Raw: "keep raw"}); err != nil {
		t.Fatal(err)
	}
	if err := a.deleteUserAPRSHistory("ea7klk"); err != nil {
		t.Fatal(err)
	}
	if got := len(a.loadSentHistory("EA7KLK")); got != 0 {
		t.Fatalf("deleted user's sent APRS count = %d, want 0", got)
	}
	if got := len(a.loadReceivedHistory("EA7KLK")); got != 0 {
		t.Fatalf("deleted user's received APRS count = %d, want 0", got)
	}
	var partCount int64
	if err := a.db.Model(&dbAPRSSentPart{}).Count(&partCount).Error; err != nil {
		t.Fatal(err)
	}
	if partCount != 0 {
		t.Fatalf("deleted user's APRS sent parts count = %d, want 0", partCount)
	}
	if got := len(a.loadSentHistory("EA1ABC")); got != 1 {
		t.Fatalf("other user's sent APRS count = %d, want 1", got)
	}
	if got := len(a.loadReceivedHistory("EA1ABC")); got != 1 {
		t.Fatalf("other user's received APRS count = %d, want 1", got)
	}
}
