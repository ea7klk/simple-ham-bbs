package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Account flows that need email: activation, password reset, email change.

// activationPrompt sends an activation code and asks for it until it is correct,
// the caller cancels, or the attempts run out.
//
// A wrong code is reported and countd in the database, so closing and reopening
// the connection does not reset the attempts. The caller may request a new code
// at any point, which replaces the old one.
func (a *app) activateAccount(callsign string, profile userProfile, lang string) (userProfile, bool) {
	if profile.Activated {
		return profile, true
	}
	if !a.mail.Enabled() {
		// Without a mail path there is no way to deliver a code. Rather than
		// locking every caller out of a deployment that has no SMTP, activation
		// is skipped and said so in the log.
		a.logBBSAction(callsign, "activation_skipped", "reason=%q", "mail is not configured")
		profile.Activated = true
		return profile, true
	}
	if strings.TrimSpace(profile.Email) == "" {
		a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{errorStyle.Render(a.t(lang, "activation_no_email"))}})
		return profile, false
	}

	send := func() bool {
		code, err := a.issueActivationCode(callsign, time.Now())
		if err != nil {
			a.showMutationError(lang, a.t(lang, "activation_title"), err)
			return false
		}
		msg := activationMail(lang, callsign, code, a.cfg.name)
		msg.To = profile.Email
		if err := a.mail.Send(msg); err != nil {
			a.logBBSAction(callsign, "activation_mail_failed", "to=%q error=%q", profile.Email, err.Error())
			a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{errorStyle.Render(a.t(lang, "activation_mail_failed"))}})
			return false
		}
		a.logBBSAction(callsign, "activation_mail_sent", "to=%q", profile.Email)
		return true
	}

	if !send() {
		return profile, false
	}
	for {
		action, values, ok := a.runForm(lang, a.t(lang, "activation_title")+" - "+callsign, []formField{
			{
				name: "code", label: a.t(lang, "activation_code"), required: true, limit: 12,
				normalizer: normalizeActivationCode,
			},
		}, []string{"activate", "resend", "cancel"})
		if !ok || action == "cancel" {
			return profile, false
		}
		if action == "resend" {
			if !send() {
				return profile, false
			}
			a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{a.t(lang, "activation_resent")}})
			continue
		}
		err := a.verifyActivationCode(callsign, values["code"], time.Now())
		switch {
		case err == nil:
			profile.Activated = true
			a.logBBSAction(callsign, "activation_confirmed", "")
			return profile, true
		case errors.Is(err, errActivationTooMany):
			a.logBBSAction(callsign, "activation_exhausted", "")
			a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{errorStyle.Render(a.t(lang, "activation_too_many"))}})
			return profile, false
		case errors.Is(err, errActivationExpired):
			a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{errorStyle.Render(a.t(lang, "activation_expired"))}})
			if !send() {
				return profile, false
			}
		default:
			a.showInfo(lang, a.t(lang, "activation_title"), [][]string{{errorStyle.Render(a.t(lang, "activation_wrong"))}})
		}
	}
}

// forgotPassword runs the reset request from the login screen.
//
// The answer is the same whether or not the account exists: this is the one
// place where a difference would be useful to an attacker, and it costs nothing
// to keep quiet.
func (a *app) forgotPassword(lang string) {
	_, values, ok := a.runForm(lang, a.t(lang, "forgot_password_title"), []formField{
		{
			name: "identifier", label: a.t(lang, "forgot_password_prompt"), required: true, limit: 120,
			normalizer: sanitizeSingleLineText,
		},
	}, []string{"send", "cancel"})
	if !ok || values == nil {
		return
	}
	identifier := values["identifier"]
	a.logBBSAction("", "password_reset_requested", "identifier=%q", identifier)

	if !a.mail.Enabled() {
		a.showInfo(lang, a.t(lang, "forgot_password_title"), [][]string{{a.t(lang, "mail_not_configured")}})
		return
	}
	if err := a.sendPasswordReset(identifier); err != nil {
		a.logBBSAction("", "password_reset_failed", "identifier=%q error=%q", identifier, err.Error())
	}
	a.showInfo(lang, a.t(lang, "forgot_password_title"), [][]string{{a.t(lang, "forgot_password_sent")}})
}

// sendPasswordReset issues and sends a reset link when the identifier matches an
// active account. Unknown identifiers, disabled accounts and over-quota requests
// are silent no-ops.
func (a *app) sendPasswordReset(identifier string) error {
	profile, callsign, found, err := a.lookupAccount(identifier)
	if err != nil {
		return err
	}
	if !found || profile.Disabled || strings.TrimSpace(profile.Email) == "" {
		return nil
	}
	count, err := a.recentTokens(tokenPurposePasswordReset, callsign, time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	if count >= resetRequestsPerHour {
		return fmt.Errorf("reset quota reached for %s", callsign)
	}
	token, err := a.issueToken(tokenPurposePasswordReset, callsign, profile.Email, "", passwordResetTTL, time.Now())
	if err != nil {
		return err
	}
	lang := profile.Language
	if lang == "" {
		lang = "en"
	}
	msg := passwordResetMail(lang, callsign, a.mail.WebURL("/reset?token="+token), a.cfg.name)
	msg.To = profile.Email
	if err := a.mail.Send(msg); err != nil {
		return err
	}
	a.logBBSAction(callsign, "password_reset_mail_sent", "to=%q expires_in=%s", profile.Email, passwordResetTTL)
	return nil
}

// emailLookup is the one query that finds an account by address.
//
// It names COLLATE NOCASE deliberately: SQLite only uses the collated index when
// the query asks for the collation too, and a bare `email = ?` scans the table —
// 132 ms at ten thousand users. It lives on its own so a test can assert that the
// query the code actually builds is the indexed one, rather than asserting on a
// copy of the SQL written in the test.
func emailLookup(db *gorm.DB, identifier string) *gorm.DB {
	return db.Where("email = ? COLLATE NOCASE", strings.TrimSpace(identifier))
}

// lookupAccount resolves a callsign or an email address to an account.
func (a *app) lookupAccount(identifier string) (userProfile, string, bool, error) {
	identifier = sanitizeSingleLineText(identifier)
	if identifier == "" {
		return userProfile{}, "", false, nil
	}
	key := normalizeCallsign(identifier)
	if profile, ok, err := a.loadUser(key); err != nil {
		return userProfile{}, "", false, err
	} else if ok {
		return profile, key, true, nil
	}
	if !strings.Contains(identifier, "@") {
		return userProfile{}, "", false, nil
	}
	row := dbUser{}
	err := emailLookup(a.db, identifier).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return userProfile{}, "", false, nil
	}
	if err != nil {
		return userProfile{}, "", false, err
	}
	return profileFromRow(row), row.Callsign, true, nil
}

// beginEmailChange starts the double confirmation and reports what was sent.
//
// Two tokens share one change ID: one goes to the current address (approve) and
// one to the new address (validate). Both must be consumed before the address
// changes, which stops a stolen session from silently redirecting mail.
func (a *app) beginEmailChange(callsign, oldEmail, newEmail, lang string) error {
	oldEmail = strings.TrimSpace(oldEmail)
	newEmail = strings.TrimSpace(newEmail)
	if oldEmail == newEmail {
		return nil
	}
	changeID := newChangeID()

	oldToken, err := a.issueToken(tokenPurposeEmailOld, callsign, newEmail, changeID, emailChangeTTL, time.Now())
	if err != nil {
		return err
	}
	newToken, err := a.issueToken(tokenPurposeEmailNew, callsign, newEmail, changeID, emailChangeTTL, time.Now())
	if err != nil {
		return err
	}

	confirm := emailChangeMail(lang, callsign, a.mail.WebURL("/email/confirm?token="+oldToken), a.cfg.name, newEmail, true)
	confirm.To = oldEmail
	if err := a.mail.Send(confirm); err != nil {
		return fmt.Errorf("confirm mail to current address: %w", err)
	}
	validate := emailChangeMail(lang, callsign, a.mail.WebURL("/email/confirm?token="+newToken), a.cfg.name, newEmail, false)
	validate.To = newEmail
	if err := a.mail.Send(validate); err != nil {
		return fmt.Errorf("validation mail to new address: %w", err)
	}
	a.logBBSAction(callsign, "email_change_requested", "from=%q to=%q expires_in=%s", oldEmail, newEmail, emailChangeTTL)
	return nil
}

func newChangeID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// A collision here would only group two unrelated changes; fall back to
		// a timestamp rather than failing the request.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
