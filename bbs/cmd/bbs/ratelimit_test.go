package main

import (
	"strings"
	"testing"
	"time"
)

func TestClientAddressFromSSHConnection(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"absent", "", "unknown"},
		{"blank", "   ", "unknown"},
		{"ipv4", "203.0.113.7 51514 10.42.0.149 2222", "203.0.113.7"},
		{"ipv6", "2001:db8::1 51514 10.42.0.149 2222", "2001:db8::1"},
		{"nonsense", "x", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSH_CONNECTION", tc.env)
			if got := clientAddress(); got != tc.want {
				t.Fatalf("clientAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordAuthFailureLocksAfterLimit(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	for i := 1; i < authFailureLimit; i++ {
		locked, err := a.recordAuthFailure("203.0.113.7", at)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			t.Fatalf("locked at attempt %d, limit is %d", i, authFailureLimit)
		}
	}
	locked, err := a.recordAuthFailure("203.0.113.7", at)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatalf("no lockout at attempt %d", authFailureLimit)
	}

	remaining, err := a.authLockedRemaining("203.0.113.7", at)
	if err != nil {
		t.Fatal(err)
	}
	if remaining <= 0 || remaining > authLockoutDuration {
		t.Fatalf("lockout remaining = %s", remaining)
	}
}

func TestAuthLockExpires(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < authFailureLimit; i++ {
		if _, err := a.recordAuthFailure("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	later := at.Add(authLockoutDuration + time.Second)
	remaining, err := a.authLockedRemaining("203.0.113.7", later)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("lockout outlived its window: %s", remaining)
	}
}

func TestAuthFailuresArePerAddress(t *testing.T) {
	a := testApp(t)
	at := time.Unix(0, 0)
	for i := 0; i < authFailureLimit; i++ {
		if _, err := a.recordAuthFailure("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := a.authLockedRemaining("198.51.100.9", at)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("one address's failures locked another address")
	}
}

func TestAuthFailuresResetAfterWindow(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < authFailureLimit-1; i++ {
		if _, err := a.recordAuthFailure("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	// A gap longer than the window forgets earlier attempts.
	later := at.Add(authFailureWindow + time.Minute)
	locked, err := a.recordAuthFailure("203.0.113.7", later)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("failures carried across the window boundary")
	}
}

func TestSuccessfulLoginClearsAuthFailures(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < authFailureLimit-1; i++ {
		if _, err := a.recordAuthFailure("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.clearAuthFailures("203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	locked, err := a.recordAuthFailure("203.0.113.7", at)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("counter was not cleared by the successful login")
	}
}

func TestPruneAuthFailuresDropsIdleCounters(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if err := a.db.Create(&dbAuthFailure{Address: "203.0.113.7", Failures: 1, WindowStart: at.Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbAuthFailure{Address: "198.51.100.9", Failures: 1, WindowStart: at.Add(-authFailureRetention - time.Hour).Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.pruneAuthFailures(at); err != nil {
		t.Fatal(err)
	}
	var rows []dbAuthFailure
	if err := a.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Address != "203.0.113.7" {
		t.Fatalf("prune kept %#v", rows)
	}
}

// A second session from the same address hits the limit the first one built up.
func TestLockoutIsSharedAcrossSessions(t *testing.T) {
	first := testApp(t)
	second := sessionPair(t, first)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	for i := 0; i < authFailureLimit; i++ {
		if _, err := first.recordAuthFailure("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := second.authLockedRemaining("203.0.113.7", at)
	if err != nil {
		t.Fatal(err)
	}
	if remaining <= 0 {
		t.Fatal("lockout was not visible from another session")
	}
}

// The full login path refuses a locked address before showing the form again.
func TestAuthenticateRefusesLockedAddress(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51514 10.42.0.149 2222")

	for i := 0; i < authFailureLimit; i++ {
		if _, err := a.recordAuthFailure("203.0.113.7", time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	formCalls := 0
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		formCalls++
		return "cancel", nil, false
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	_, _, err := a.authenticate()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "too many") {
		t.Fatalf("authenticate() error = %v, want a lockout refusal", err)
	}
	if formCalls != 0 {
		t.Fatalf("login form was shown %d time(s) to a locked address", formCalls)
	}
}

// Failed attempts through authenticate() feed the shared counter.
func TestFailedLoginsAccumulateAcrossSessions(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51514 10.42.0.149 2222")
	if err := a.saveUser("EA7KLK", userProfile{FullName: "Volker", Email: "v@example.com", Language: "en", PasswordHash: hashPassword("rightpass")}); err != nil {
		t.Fatal(err)
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	attempt := func() error {
		queue := []struct {
			action string
			values map[string]string
			ok     bool
		}{
			{"submit", map[string]string{"callsign": "EA7KLK", "password": "wrong"}, true},
			{"submit", map[string]string{"callsign": "EA7KLK", "password": "wrong"}, true},
			{"submit", map[string]string{"callsign": "EA7KLK", "password": "wrong"}, true},
		}
		a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
			if len(queue) == 0 {
				return "cancel", nil, false
			}
			next := queue[0]
			queue = queue[1:]
			return next.action, next.values, next.ok
		}
		_, _, err := a.authenticate()
		return err
	}

	// One session burns its three per-callsign attempts.
	if err := attempt(); err == nil {
		t.Fatal("expected the per-session limit to trigger")
	}
	row := dbAuthFailure{}
	if err := a.db.Where("address = ?", "203.0.113.7").Take(&row).Error; err != nil {
		t.Fatalf("no failure recorded for the address: %v", err)
	}
	if row.Failures != 3 {
		t.Fatalf("recorded failures = %d, want 3", row.Failures)
	}

	// A fresh session must not reset the address's counter; the next attempts
	// reach the shared limit.
	if err := attempt(); err == nil {
		t.Fatal("expected the shared limit to trigger")
	}
	remaining, err := a.authLockedRemaining("203.0.113.7", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if remaining <= 0 {
		t.Fatal("address not locked after repeated sessions")
	}
}

func TestRegistrationLimitIsPerAddressWindow(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	for i := 0; i < registrationLimit; i++ {
		allowed, err := a.registrationAllowed("203.0.113.7", at)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("blocked at registration %d of %d", i+1, registrationLimit)
		}
		if err := a.recordRegistration("203.0.113.7", at); err != nil {
			t.Fatal(err)
		}
	}
	allowed, err := a.registrationAllowed("203.0.113.7", at)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatalf("allowed a registration past the limit of %d", registrationLimit)
	}

	// Another address keeps its own budget.
	if ok, err := a.registrationAllowed("198.51.100.9", at); err != nil || !ok {
		t.Fatalf("second address allowed=%v err=%v", ok, err)
	}

	// A fresh window resets the count.
	later := at.Add(registrationWindow + time.Minute)
	if ok, err := a.registrationAllowed("203.0.113.7", later); err != nil || !ok {
		t.Fatalf("address still limited after the window: allowed=%v err=%v", ok, err)
	}
}

func TestRegistrationIsNotChargedUntilRecorded(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for i := 0; i < registrationLimit*2; i++ {
		allowed, err := a.registrationAllowed("203.0.113.7", at)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("a cancelled registration charged the budget at check %d", i+1)
		}
	}
}

func TestPruneRegistrationsDropsIdleCounters(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	if err := a.db.Create(&dbRegistration{Address: "203.0.113.7", Count: 1, WindowStart: at.Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbRegistration{Address: "198.51.100.9", Count: 9, WindowStart: at.Add(-registrationRetention - time.Hour).Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.pruneRegistrations(at); err != nil {
		t.Fatal(err)
	}
	var rows []dbRegistration
	if err := a.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Address != "203.0.113.7" {
		t.Fatalf("prune kept %#v", rows)
	}
}

// The limit is enforced on the real registration path, and the account it
// refuses is never created.
func TestAuthenticateBlocksRegistrationPastLimit(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.text["en"]["registration_rate_limited"] = "Too many new accounts from this address. Try again later."
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51514 10.42.0.149 2222")
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	for i := 0; i < registrationLimit; i++ {
		if err := a.recordRegistration("203.0.113.7", time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	formCalls := 0
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		formCalls++
		return "submit", map[string]string{"callsign": "EA7KLK", "password": ""}, true
	}
	_, _, err := a.authenticate()
	if err == nil || !strings.Contains(err.Error(), "Too many new accounts") {
		t.Fatalf("authenticate() error = %v, want the registration limit", err)
	}
	users, loadErr := a.loadUsers()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := users["EA7KLK"]; ok {
		t.Fatal("account was created despite the limit")
	}
	// Only the login form is drawn; the registration form never opens.
	if formCalls != 1 {
		t.Fatalf("forms drawn = %d, want 1", formCalls)
	}
}

// A successful registration consumes exactly one slot.
func TestAuthenticateChargesOneRegistration(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	t.Setenv("SSH_CONNECTION", "203.0.113.7 51514 10.42.0.149 2222")
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	queue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "EA7KLK", "password": ""}, true},
		{"save", formValues(nil), true},
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := queue[0]
		queue = queue[1:]
		return next.action, next.values, next.ok
	}
	if _, _, err := a.authenticate(); err != nil {
		t.Fatal(err)
	}
	row := dbRegistration{}
	if err := a.db.Where("address = ?", "203.0.113.7").Take(&row).Error; err != nil {
		t.Fatalf("no registration recorded: %v", err)
	}
	if row.Count != 1 {
		t.Fatalf("registrations recorded = %d, want 1", row.Count)
	}
}
