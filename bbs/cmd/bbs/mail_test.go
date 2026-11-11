package main

import (
	"strings"
	"testing"
	"time"
)

// captureMailer installs a mailer that records messages instead of sending them.
func captureMailer(t *testing.T, a *app) *[]mailMessage {
	t.Helper()
	sent := &[]mailMessage{}
	a.mail = &mailer{
		cfg: mailConfig{
			host:       "mail.example.org",
			port:       587,
			from:       "bbs@example.org",
			fromName:   "Test BBS",
			security:   "starttls",
			webBaseURL: "https://bbs.ea7lk.es",
		},
		now: func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) },
		send: func(msg mailMessage) error {
			*sent = append(*sent, msg)
			return nil
		},
	}
	return sent
}

func TestMailerDisabledWithoutHost(t *testing.T) {
	m := &mailer{cfg: mailConfig{from: "bbs@example.org"}}
	if m.Enabled() {
		t.Fatal("mailer enabled without a host")
	}
	if err := m.Send(mailMessage{To: "a@example.org"}); err == nil {
		t.Fatal("Send succeeded on a disabled mailer")
	}
	var nilMailer *mailer
	if nilMailer.Enabled() {
		t.Fatal("nil mailer reported enabled")
	}
	if err := nilMailer.Send(mailMessage{To: "a@example.org"}); err == nil {
		t.Fatal("nil mailer Send returned no error")
	}
	if got := nilMailer.WebURL("/reset"); got != "https://bbs.ea7lk.es/reset" {
		t.Fatalf("nil mailer WebURL = %q", got)
	}
}

func TestMailerWebURLTrimsTrailingSlash(t *testing.T) {
	m := &mailer{cfg: mailConfig{webBaseURL: "https://bbs.ea7lk.es/"}}
	if got := m.WebURL("/reset?token=abc"); got != "https://bbs.ea7lk.es/reset?token=abc" {
		t.Fatalf("WebURL = %q", got)
	}
}

func TestMailRenderHeadersAndEncoding(t *testing.T) {
	m := &mailer{cfg: mailConfig{from: "bbs@example.org", fromName: "Funkbörse"}, now: time.Now}
	raw := m.render(mailMessage{To: "user@example.org", Subject: "Grüße", Body: "line one\nline two"})

	for _, want := range []string{
		"To: <user@example.org>\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"Auto-Submitted: auto-generated\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("rendered message missing %q:\n%s", want, raw)
		}
	}
	// Non-ASCII subject and display name are encoded, not sent raw.
	if strings.Contains(raw, "Grüße") {
		t.Fatalf("subject was not encoded: %s", raw)
	}
	if !strings.Contains(raw, "Subject: =?utf-8?") {
		t.Fatalf("subject missing encoded-word: %s", raw)
	}
	if !strings.Contains(raw, "Funkbörse") && !strings.Contains(raw, "=?utf-8?") {
		t.Fatalf("display name neither encoded nor preserved: %s", raw)
	}
	// Every body line ends CRLF.
	if !strings.Contains(raw, "line one\r\nline two\r\n") {
		t.Fatalf("body line endings not normalised: %q", raw)
	}
}

func TestMailRenderDotStuffsBody(t *testing.T) {
	m := &mailer{cfg: mailConfig{from: "bbs@example.org"}, now: time.Now}
	raw := m.render(mailMessage{To: "user@example.org", Subject: "s", Body: ".hidden"})
	if !strings.Contains(raw, "\r\n..hidden\r\n") {
		t.Fatalf("leading dot was not stuffed: %q", raw)
	}
}

func TestFormatAddress(t *testing.T) {
	cases := []struct{ name, addr, want string }{
		{"", "a@example.org", "<a@example.org>"},
		{"Plain", "a@example.org", "Plain <a@example.org>"},
		{"Smith, John", "a@example.org", `"Smith, John" <a@example.org>`},
	}
	for _, tc := range cases {
		if got := formatAddress(tc.name, tc.addr); got != tc.want {
			t.Fatalf("formatAddress(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := formatAddress("Ünïcode", "a@example.org"); !strings.HasPrefix(got, "=?utf-8?") {
		t.Fatalf("non-ASCII name not encoded: %q", got)
	}
}

// Wrapping must not break a URL in half: a link split across lines is a broken
// link in most mail clients.
func TestWrapBodyKeepsLongTokensIntact(t *testing.T) {
	link := "https://bbs.ea7lk.es/reset?token=" + strings.Repeat("a", 60)
	body := wrapBody("Open this link to choose a new password:\n\n"+link+"\n\nThanks", 40)
	if !strings.Contains(body, link) {
		t.Fatalf("URL was split by the wrapper:\n%s", body)
	}
}

func TestActivationCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, hash, err := newActivationCode("EA7KLK")
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != activationCodeLength {
			t.Fatalf("code %q has length %d", code, len(code))
		}
		for _, r := range code {
			if !strings.ContainsRune(activationAlphabet, r) {
				t.Fatalf("code %q contains %q, which is outside the alphabet", code, r)
			}
		}
		if hash != activationHash("EA7KLK", strings.ToLower(code)) {
			t.Fatal("verification is not case-insensitive")
		}
		if hash == activationHash("DL1ABC", code) {
			t.Fatal("a code verifies against a different callsign")
		}
	}
}

func TestActivationVerifyLifecycle(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	code, err := a.issueActivationCode("EA7KLK", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.verifyActivationCode("EA7KLK", strings.ToLower(code), at); err != nil {
		t.Fatalf("correct code rejected: %v", err)
	}
	// The code is single use.
	if err := a.verifyActivationCode("EA7KLK", code, at); err == nil {
		t.Fatal("a consumed code verified twice")
	}
}

func TestActivationWrongCodeBurnsAttempts(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if _, err := a.issueActivationCode("EA7KLK", at); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < activationAttempts; i++ {
		if err := a.verifyActivationCode("EA7KLK", "ZZZZ", at); err == nil {
			t.Fatalf("attempt %d accepted a wrong code", i+1)
		}
	}
	if err := a.verifyActivationCode("EA7KLK", "ZZZZ", at); err != errActivationTooMany {
		t.Fatalf("after %d attempts error = %v, want errActivationTooMany", activationAttempts+1, err)
	}
	// Exhaustion discards the code, so even the right one no longer works.
	row := dbActivation{}
	if err := a.db.Where("callsign = ?", "EA7KLK").Take(&row).Error; err == nil {
		t.Fatal("exhausted activation row survived")
	}
}

func TestActivationCodeExpires(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	code, err := a.issueActivationCode("EA7KLK", at)
	if err != nil {
		t.Fatal(err)
	}
	later := at.Add(activationCodeTTL + time.Minute)
	if err := a.verifyActivationCode("EA7KLK", code, later); err != errActivationExpired {
		t.Fatalf("expired code error = %v, want errActivationExpired", err)
	}
}

// Registration sends a code, and the account is not activated until it is
// entered correctly.
func TestActivationFlowThroughRegistration(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	captureMailer(t, a)
	a.text["en"]["activation_title"] = "Activation"
	a.text["en"]["activation_code"] = "Code"
	a.text["en"]["activation_wrong"] = "Wrong code."
	a.text["en"]["activation_notice"] = "A code will be emailed."
	a.text["en"]["activate_button"] = "Activate"
	a.text["en"]["resend_button"] = "Resend"
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	var code string
	orig := a.mail.send
	a.mail.send = func(msg mailMessage) error {
		// The code sits on its own indented line in the activation mail.
		for _, line := range strings.Split(msg.Body, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) == activationCodeLength && strings.IndexFunc(trimmed, func(r rune) bool {
				return !strings.ContainsRune(activationAlphabet, r)
			}) < 0 {
				code = trimmed
			}
		}
		return orig(msg)
	}

	step := 0
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		step++
		switch step {
		case 1:
			return "submit", map[string]string{"callsign": "EA7KLK", "password": ""}, true
		case 2:
			return "save", formValues(nil), true
		case 3:
			// A wrong code first, to prove retries are allowed.
			return "activate", map[string]string{"code": "ZZZZ"}, true
		default:
			if code == "" {
				t.Fatal("no activation code was emailed before the prompt")
			}
			return "activate", map[string]string{"code": code}, true
		}
	}

	callsign, profile, err := a.authenticate()
	if err != nil {
		t.Fatalf("authenticate() error = %v", err)
	}
	if callsign != "EA7KLK" {
		t.Fatalf("callsign = %q", callsign)
	}
	if !profile.Activated {
		t.Fatalf("profile not activated: %#v", profile)
	}
	stored, _, err := a.loadUser("EA7KLK")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Activated {
		t.Fatal("activation was not persisted")
	}
	if step < 4 {
		t.Fatalf("only %d form steps ran; the wrong code was not retried", step)
	}
}

// With mail configured, an unactivated account is challenged on login.
func TestUnactivatedAccountIsChallengedOnLogin(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	captureMailer(t, a)
	a.text["en"]["activation_title"] = "Activation"
	a.text["en"]["activation_code"] = "Code"
	a.text["en"]["activation_wrong"] = "Wrong code."
	a.text["en"]["activate_button"] = "Activate"
	a.text["en"]["resend_button"] = "Resend"
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "v@example.org", Language: "en",
		PasswordHash: hashPassword("password1"), Activated: false,
	}); err != nil {
		t.Fatal(err)
	}

	forms := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"login", map[string]string{"callsign": "EA7KLK", "password": "password1"}, true},
		{"cancel", nil, false}, // give up on activation
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := forms[0]
		forms = forms[1:]
		return next.action, next.values, next.ok
	}
	if _, _, err := a.authenticate(); err == nil {
		t.Fatal("login succeeded for an unactivated account")
	}
	stored, _, _ := a.loadUser("EA7KLK")
	if stored.Activated {
		t.Fatal("cancelling activation marked the account activated")
	}
}

// Without SMTP, activation is skipped so an unconfigured deployment still works.
func TestActivationSkippedWhenMailUnconfigured(t *testing.T) {
	a := testApp(t)
	a.cfg.bbsLogFile = t.TempDir() + "/bbs.log"
	a.currentUser = ""
	profile, ok := a.activateAccount("EA7KLK", userProfile{Email: "v@example.org", Language: "en"}, "en")
	if !ok || !profile.Activated {
		t.Fatalf("activation not skipped: ok=%v profile=%#v", ok, profile)
	}
}

func TestForgotPasswordSendsLinkInUserLanguage(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	sent := captureMailer(t, a)
	a.text["de"] = map[string]any{"forgot_password_title": "Passwort vergessen", "forgot_password_sent": "Gesendet."}
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "v@example.org", Language: "de",
		PasswordHash: hashPassword("password1"), Activated: true,
	}); err != nil {
		t.Fatal(err)
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"identifier": "ea7klk"}, true
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	a.forgotPassword("en")
	if len(*sent) != 1 {
		t.Fatalf("reset mails = %d, want 1", len(*sent))
	}
	msg := (*sent)[0]
	if msg.To != "v@example.org" {
		t.Fatalf("reset mail recipient = %q", msg.To)
	}
	if !strings.Contains(msg.Subject, "Passwort") {
		t.Fatalf("reset mail not in the user's language: %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "https://bbs.ea7lk.es/reset?token=") {
		t.Fatalf("reset mail has no tokenised link:\n%s", msg.Body)
	}

	// The token exists, is single-purpose, and expires in two hours.
	tokens := []dbToken{}
	if err := a.db.Where("purpose = ?", tokenPurposePasswordReset).Find(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("reset tokens = %d", len(tokens))
	}
	ttl := time.Unix(tokens[0].ExpiresAt, 0).Sub(time.Unix(tokens[0].CreatedAt, 0))
	if ttl != passwordResetTTL {
		t.Fatalf("reset token ttl = %s, want %s", ttl, passwordResetTTL)
	}
	if tokens[0].Callsign != "EA7KLK" {
		t.Fatalf("token callsign = %q", tokens[0].Callsign)
	}
}

// An unknown identifier gets the same answer and no mail.
func TestForgotPasswordIsSilentForUnknownAccounts(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	sent := captureMailer(t, a)
	var shown [][]string
	a.showInfoHook = func(lang, title string, rows [][]string) { shown = rows }
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"identifier": "NOSUCH"}, true
	}
	a.forgotPassword("en")
	if len(*sent) != 0 {
		t.Fatalf("mail sent for an unknown identifier: %#v", *sent)
	}
	if len(shown) == 0 {
		t.Fatal("no confirmation shown for an unknown identifier")
	}
}

func TestForgotPasswordByEmailAddress(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	captureMailer(t, a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "Volker@Example.org", Language: "en",
		PasswordHash: hashPassword("password1"), Activated: true,
	}); err != nil {
		t.Fatal(err)
	}
	profile, callsign, found, err := a.lookupAccount("volker@example.ORG")
	if err != nil || !found || callsign != "EA7KLK" {
		t.Fatalf("lookupAccount by email = %q, %v, %v", callsign, found, err)
	}
	if profile.Email != "Volker@Example.org" {
		t.Fatalf("profile = %#v", profile)
	}
}

func TestForgotPasswordRateLimited(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	sent := captureMailer(t, a)
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "v@example.org", Language: "en",
		PasswordHash: hashPassword("password1"), Activated: true,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < resetRequestsPerHour+2; i++ {
		if err := a.sendPasswordReset("EA7KLK"); err != nil && i < resetRequestsPerHour {
			t.Fatalf("request %d failed early: %v", i+1, err)
		}
	}
	if len(*sent) != resetRequestsPerHour {
		t.Fatalf("mails sent = %d, want %d", len(*sent), resetRequestsPerHour)
	}
}

// An email change sends two links with different recipients and one shared
// change ID, and leaves the address alone until both are confirmed.
func TestEmailChangeNeedsBothConfirmations(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	sent := captureMailer(t, a)
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "old@example.org", Language: "en",
		PasswordHash: hashPassword("password1"), Activated: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.beginEmailChange("EA7KLK", "old@example.org", "new@example.org", "en"); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 2 {
		t.Fatalf("emails sent = %d, want 2", len(*sent))
	}
	recipients := map[string]bool{}
	for _, msg := range *sent {
		recipients[msg.To] = true
		if !strings.Contains(msg.Body, "https://bbs.ea7lk.es/email/confirm?token=") {
			t.Fatalf("change mail has no tokenised link:\n%s", msg.Body)
		}
	}
	if !recipients["old@example.org"] || !recipients["new@example.org"] {
		t.Fatalf("recipients = %v, want both addresses", recipients)
	}

	tokens := []dbToken{}
	if err := a.db.Where("purpose IN ?", []string{tokenPurposeEmailOld, tokenPurposeEmailNew}).Find(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("change tokens = %d, want 2", len(tokens))
	}
	if tokens[0].ChangeID == "" || tokens[0].ChangeID != tokens[1].ChangeID {
		t.Fatalf("tokens do not share a change id: %#v", tokens)
	}
	for _, row := range tokens {
		ttl := time.Unix(row.ExpiresAt, 0).Sub(time.Unix(row.CreatedAt, 0))
		if ttl != emailChangeTTL {
			t.Fatalf("change token ttl = %s, want %s", ttl, emailChangeTTL)
		}
	}

	// One confirmation is not enough.
	if err := a.db.Model(&dbToken{}).Where("purpose = ?", tokenPurposeEmailOld).UpdateColumn("consumed_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.applyEmailChanges(time.Now()); err != nil {
		t.Fatal(err)
	}
	profile, _, _ := a.loadUser("EA7KLK")
	if profile.Email != "old@example.org" {
		t.Fatalf("address changed after one confirmation: %q", profile.Email)
	}

	// Both confirmations apply it.
	if err := a.db.Model(&dbToken{}).Where("purpose = ?", tokenPurposeEmailNew).UpdateColumn("consumed_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.applyEmailChanges(time.Now()); err != nil {
		t.Fatal(err)
	}
	profile, _, _ = a.loadUser("EA7KLK")
	if profile.Email != "new@example.org" {
		t.Fatalf("address after both confirmations = %q", profile.Email)
	}
	// The pair is consumed and removed.
	var remaining int64
	if err := a.db.Model(&dbToken{}).Where("change_id = ?", tokens[0].ChangeID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("change tokens left behind: %d", remaining)
	}
}

// A change whose links have expired is discarded rather than left half-done.
func TestExpiredEmailChangeIsDiscarded(t *testing.T) {
	a := testApp(t)
	if err := a.saveUser("EA7KLK", userProfile{Email: "old@example.org", Language: "en", Activated: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueToken(tokenPurposeEmailOld, "EA7KLK", "new@example.org", "chg1", time.Hour, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueToken(tokenPurposeEmailNew, "EA7KLK", "new@example.org", "chg1", time.Hour, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.applyEmailChanges(time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := a.db.Model(&dbToken{}).Where("change_id = ?", "chg1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired change was not discarded: %d rows", count)
	}
	profile, _, _ := a.loadUser("EA7KLK")
	if profile.Email != "old@example.org" {
		t.Fatalf("expired change altered the address: %q", profile.Email)
	}
}

// Changing the address through the profile form does not apply it directly when
// mail is configured.
func TestProfileChangeDefersEmailUntilConfirmed(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	captureMailer(t, a)
	a.text["en"]["email_change_pending"] = "Requested."
	a.text["en"]["email_change_pending_detail"] = "Sent to %s and the new address."
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "old@example.org", Language: "en",
		PasswordHash: hashPassword("password1"), Activated: true,
	}); err != nil {
		t.Fatal(err)
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", formValues(map[string]string{"email": "new@example.org"}), true
	}
	profile, _, _ := a.loadUser("EA7KLK")
	updated := a.changeProfile("EA7KLK", profile, "en")
	if updated.Email != "old@example.org" {
		t.Fatalf("address applied before confirmation: %q", updated.Email)
	}
	stored, _, _ := a.loadUser("EA7KLK")
	if stored.Email != "old@example.org" {
		t.Fatalf("stored address applied before confirmation: %q", stored.Email)
	}
}

// Legacy accounts stay usable: the migration marks them activated exactly once.
func TestBackfillActivatedRunsOnce(t *testing.T) {
	a := testApp(t)
	if err := a.db.Create(&dbUser{Callsign: "LEGACY", Email: "l@example.org"}).Error; err != nil {
		t.Fatal(err)
	}
	// Remove the marker so the migration runs again, as it would on upgrade.
	if err := a.db.Where("key = ?", "migrated.activation").Delete(&dbSetting{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillActivated(a.db); err != nil {
		t.Fatal(err)
	}
	legacy, _, _ := a.loadUser("LEGACY")
	if !legacy.Activated {
		t.Fatal("legacy account was not marked activated")
	}

	// A deliberately unactivated account created after the migration stays that
	// way: the marker stops a second sweep.
	if err := a.saveUser("NEWBIE", userProfile{Email: "n@example.org", Activated: false}); err != nil {
		t.Fatal(err)
	}
	if err := backfillActivated(a.db); err != nil {
		t.Fatal(err)
	}
	newbie, _, _ := a.loadUser("NEWBIE")
	if newbie.Activated {
		t.Fatal("the migration re-ran and activated a new account")
	}
}

// A column added by migration leaves NULL in existing rows, which `= false` does
// not match. That is exactly how the first version of the backfill failed in
// production while still recording its one-time marker.
func TestBackfillActivatedRepairsNullRowsEvenAfterTheMarker(t *testing.T) {
	a := testApp(t)
	if err := a.db.Exec("INSERT INTO db_users (callsign, email, activated) VALUES (?, ?, NULL)", "NULLY", "n@example.org").Error; err != nil {
		t.Fatal(err)
	}
	// openDatabase already wrote the marker, which is the state the live
	// database was in when the bug shipped.
	var markers int64
	if err := a.db.Model(&dbSetting{}).Where("key = ?", "migrated.activation").Count(&markers).Error; err != nil {
		t.Fatal(err)
	}
	if markers != 1 {
		t.Fatal("expected the one-time marker to be present")
	}

	if err := backfillActivated(a.db); err != nil {
		t.Fatal(err)
	}
	repaired, _, err := a.loadUser("NULLY")
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.Activated {
		t.Fatal("a NULL activated column was not repaired")
	}

	var nulls int64
	if err := a.db.Model(&dbUser{}).Where("activated IS NULL").Count(&nulls).Error; err != nil {
		t.Fatal(err)
	}
	if nulls != 0 {
		t.Fatalf("%d rows still NULL after the backfill", nulls)
	}
}

func TestPruneTokensDropsExpiredRows(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	if _, err := a.issueToken(tokenPurposePasswordReset, "EA7KLK", "v@example.org", "", time.Hour, now.Add(-tokenRetention-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueToken(tokenPurposePasswordReset, "EA7KLK", "v@example.org", "", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueActivationCode("EA7KLK", now.Add(-2*activationCodeTTL)); err != nil {
		t.Fatal(err)
	}
	if err := a.pruneTokens(now); err != nil {
		t.Fatal(err)
	}
	var tokens int64
	if err := a.db.Model(&dbToken{}).Count(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	if tokens != 1 {
		t.Fatalf("tokens after prune = %d, want 1", tokens)
	}
	var activations int64
	if err := a.db.Model(&dbActivation{}).Count(&activations).Error; err != nil {
		t.Fatal(err)
	}
	if activations != 0 {
		t.Fatalf("expired activation codes left behind: %d", activations)
	}
}

func TestMailTextsCoverEveryUILanguage(t *testing.T) {
	for _, lang := range languageOrder {
		texts, ok := mailTexts[lang]
		if !ok {
			t.Fatalf("no mail templates for %q", lang)
		}
		if texts.subjectActivation == "" || texts.activation == "" || texts.reset == "" ||
			texts.emailOld == "" || texts.emailNew == "" {
			t.Fatalf("incomplete templates for %q: %#v", lang, texts)
		}
	}
	// An unknown language falls back rather than sending an empty message.
	if mailText("eo").activation == "" {
		t.Fatal("unknown language produced empty mail text")
	}
}
