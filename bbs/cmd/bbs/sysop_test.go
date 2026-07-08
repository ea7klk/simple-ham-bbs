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
