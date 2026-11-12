package main

import (
	"errors"
	"time"
)

var errLoginCancelled = errors.New("login cancelled")

func (a *app) authenticate() (string, userProfile, error) {
	users, err := a.loadUsers()
	if err != nil {
		return "", userProfile{}, err
	}
	for _, callsign := range a.applyConfiguredSysops(users) {
		if err := a.saveUser(callsign, users[callsign]); err != nil {
			return "", userProfile{}, err
		}
	}
	address := clientAddress()
	failedAttempts := map[string]int{}
	for {
		// Address-level lockout, shared across sessions through the database.
		if remaining, err := a.authLockedRemaining(address, time.Now()); err == nil && remaining > 0 {
			a.logBBSAction("", "login_locked", "address=%q remaining=%s", address, remaining.Truncate(time.Second))
			return "", userProfile{}, errors.New(a.t("en", "too_many_attempts"))
		}
		// The explanation used to be every language's blurb joined into one
		// line per language and then truncated, so the end of each sentence was
		// cut off. It is now two short lines in the screen's language, shown in
		// full.
		loginTitle := a.t("en", "login_title")
		intro := a.tList("en", "login_info")
		if len(intro) > 2 {
			intro = intro[:2]
		}
		action, values, ok := a.runFormStyled("en", loginTitle, []formField{
			{
				name: "callsign", label: a.t("en", "callsign_prompt"), required: true, limit: 16,
				normalizer: normalizeCallsign, validator: func(v string) bool { return callsignRE.MatchString(v) }, invalidText: a.t("en", "invalid_callsign"),
			},
			{name: "password", label: a.t("en", "password"), kind: fieldPassword},
		}, []string{"login", "forgot"}, formStyle{Center: true, Intro: intro})
		if !ok {
			return "", userProfile{}, errLoginCancelled
		}
		if action == "forgot" {
			a.forgotPassword("en")
			continue
		}
		callsign := normalizeCallsign(values["callsign"])
		password := values["password"]
		profile, exists := users[callsign]
		if !exists {
			// A new account costs a PBKDF2 hash, so it is metered per address.
			allowed, limitErr := a.registrationAllowed(address, time.Now())
			if limitErr != nil {
				a.logBBSAction(callsign, "registration_throttle_error", "address=%q error=%q", address, limitErr.Error())
				return "", userProfile{}, limitErr
			}
			if !allowed {
				a.logBBSAction(callsign, "registration_blocked", "address=%q limit=%d window=%s", address, registrationLimit, registrationWindow)
				return "", userProfile{}, errors.New(a.t("en", "registration_rate_limited"))
			}
			profile, ok = a.register(callsign, userProfile{Language: "en"}, true)
			if !ok {
				return "", userProfile{}, errLoginCancelled
			}
			users[callsign] = profile
			if err := a.saveUser(callsign, profile); err != nil {
				return "", userProfile{}, err
			}
			if err := a.recordRegistration(address, time.Now()); err != nil {
				a.logBBSAction(callsign, "registration_throttle_error", "address=%q error=%q", address, err.Error())
			}
			profile, ok = a.activateAccount(callsign, profile, profile.Language)
			if !ok {
				// The account exists but is not activated. It is kept so the
				// caller can finish later with a fresh code.
				return "", userProfile{}, errLoginCancelled
			}
			if err := a.saveUser(callsign, profile); err != nil {
				return "", userProfile{}, err
			}
			return callsign, profile, nil
		}
		lang := profile.Language
		if lang == "" {
			lang = "en"
		}
		if profile.Disabled {
			return "", userProfile{}, errors.New(a.t(lang, "user_disabled"))
		}
		if profile.PasswordHash == "" {
			profile, ok = a.register(callsign, profile, true)
			if !ok {
				return "", userProfile{}, errLoginCancelled
			}
			users[callsign] = profile
			if err := a.saveUser(callsign, profile); err != nil {
				return "", userProfile{}, err
			}
			return callsign, profile, nil
		}
		if verifyPassword(password, profile.PasswordHash) {
			// Activation runs only after the password checks out, so an
			// unactivated account cannot be claimed by whoever guesses the
			// callsign first.
			if !profile.Activated {
				profile, ok = a.activateAccount(callsign, profile, lang)
				if !ok {
					return "", userProfile{}, errLoginCancelled
				}
				users[callsign] = profile
				if err := a.saveUser(callsign, profile); err != nil {
					return "", userProfile{}, err
				}
			}
			if !profileComplete(profile) {
				profile, ok = a.register(callsign, profile, false)
				if !ok {
					return "", userProfile{}, errLoginCancelled
				}
			}
			profile.LastSeen = now()
			users[callsign] = profile
			if err := a.saveUser(callsign, profile); err != nil {
				return "", userProfile{}, err
			}
			// A successful login clears the address's failures.
			if err := a.clearAuthFailures(address); err != nil {
				a.logBBSAction(callsign, "ratelimit_clear_failed", "address=%q error=%q", address, err.Error())
			}
			return callsign, profile, nil
		}
		failedAttempts[callsign]++
		locked, lockErr := a.recordAuthFailure(address, time.Now())
		if lockErr != nil {
			// Never fail open on a broken counter, but do not lock the operator
			// out of their own BBS either: log it and fall back to the
			// per-session limit below.
			a.logBBSAction(callsign, "ratelimit_error", "address=%q error=%q", address, lockErr.Error())
		}
		if locked {
			a.logBBSAction(callsign, "login_lockout", "address=%q limit=%d window=%s", address, authFailureLimit, authFailureWindow)
			return "", userProfile{}, errors.New(a.t(lang, "too_many_attempts"))
		}
		if failedAttempts[callsign] >= 3 {
			return "", userProfile{}, errors.New(a.t(lang, "too_many_attempts"))
		}
		a.showInfo(lang, a.t(lang, "password"), [][]string{{errorStyle.Render(a.t(lang, "wrong_password"))}})
	}
}

func (a *app) register(callsign string, profile userProfile, forcePassword bool) (userProfile, bool) {
	if profile.Language == "" {
		profile.Language = "en"
	}
	if profile.FirstSeen == "" {
		profile.FirstSeen = now()
	}
	if profile.Registered == "" {
		// An account created before this field existed takes its registration
		// date from the first-seen stamp, which was set at creation too.
		profile.Registered = profile.FirstSeen
	}
	profile.IsSysop = profile.IsSysop || a.isBootstrapSysop(callsign)
	for {
		fields := a.profileFields(profile, true, forcePassword || profile.PasswordHash == "")
		title := a.t(profile.Language, "registration_form_title") + " - " + callsign
		if a.mail.Enabled() {
			// Say it before the form, not after submitting it: the address typed
			// here is where the activation code will be sent.
			title += "\n" + dimStyle.Render(a.t(profile.Language, "activation_notice"))
		}
		_, values, ok := a.runForm(profile.Language, title, fields, []string{"save", "cancel"})
		if !ok {
			return profile, false
		}
		profile = applyProfileValues(profile, values)
		if (forcePassword || profile.PasswordHash == "") && values["new_password"] == "" {
			a.showInfo(profile.Language, a.t(profile.Language, "password"), [][]string{{errorStyle.Render(a.t(profile.Language, "password_required"))}})
			continue
		}
		if values["new_password"] != "" {
			profile.PasswordHash = hashPassword(values["new_password"])
		}
		profile.LastSeen = now()
		return profile, true
	}
}

func (a *app) changeProfile(callsign string, profile userProfile, lang string) userProfile {
	users, _ := a.loadUsers()
	profile = users[callsign]
	action, values, ok := a.runForm(lang, a.t(lang, "profile_form_title")+" - "+callsign, a.profileFields(profile, false, true), []string{"save", "cancel"})
	if !ok || action == "cancel" {
		return profile
	}
	previousEmail := profile.Email
	profile = applyProfileValues(profile, values)
	if values["new_password"] != "" {
		profile.PasswordHash = hashPassword(values["new_password"])
	}
	profile.LastSeen = now()

	// An address change needs two confirmations before it takes effect. The
	// pending address is not applied here; it waits for both links.
	emailChanged := previousEmail != profile.Email
	if emailChanged && a.mail.Enabled() {
		if err := a.beginEmailChange(callsign, previousEmail, profile.Email, profile.Language); err != nil {
			a.logBBSAction(callsign, "email_change_failed", "from=%q to=%q error=%q", previousEmail, profile.Email, err.Error())
			profile.Email = previousEmail
			a.showMutationError(lang, a.t(lang, "profile_form_title"), err)
			return profile
		}
		profile.Email = previousEmail
		profile.LastSeen = now()
		if err := a.saveUser(callsign, profile); err != nil {
			a.showMutationError(lang, a.t(lang, "profile_form_title"), err)
			return profile
		}
		a.showInfo(profile.Language, a.t(profile.Language, "profile_form_title"), [][]string{
			{a.t(profile.Language, "email_change_pending")},
			{a.t(profile.Language, "email_change_pending_detail"), previousEmail},
		})
		return profile
	}
	if emailChanged {
		// Without a mail path there is nothing to confirm with. Applying the
		// change keeps an unconfigured deployment usable, and the log records
		// that it was not verified.
		a.logBBSAction(callsign, "email_change_unverified", "from=%q to=%q reason=%q", previousEmail, profile.Email, "mail is not configured")
	}
	if err := a.saveUser(callsign, profile); err != nil {
		// Do not hand the caller a profile that was never stored: it drives the
		// session language and every later screen.
		if stored, ok, loadErr := a.loadUser(callsign); loadErr == nil && ok {
			profile = stored
		}
		a.showMutationError(lang, a.t(lang, "profile_form_title"), err)
		return profile
	}
	a.showInfo(profile.Language, a.t(profile.Language, "profile_updated"), profileRows(a, profile.Language, profile))
	return profile
}

func (a *app) profileFields(profile userProfile, required, includePassword bool) []formField {
	lang := profile.Language
	if lang == "" {
		lang = "en"
	}
	fields := []formField{
		{name: "full_name", label: a.t(lang, "full_name"), value: profile.FullName, required: required, limit: 100},
		{name: "email", label: a.t(lang, "email"), value: profile.Email, required: required, limit: 120, validator: func(v string) bool { return emailRE.MatchString(v) }, invalidText: a.t(lang, "invalid_email")},
		{name: "maidenhead", label: a.t(lang, "maidenhead"), value: profile.Maidenhead, limit: 10, normalizer: normalizeLocator, validator: func(v string) bool { return v == "" || maidenheadRE.MatchString(v) }, invalidText: a.t(lang, "invalid_locator")},
		{name: "language", label: a.t(lang, "language"), kind: fieldChoice, value: lang, required: required, choices: languageOptions()},
		{name: "enable_aprs", label: a.t(lang, "enable_aprs"), kind: fieldChoice, value: boolString(profile.EnableAPRS), choices: []option{{"false", "false"}, {"true", "true"}}},
		{name: "qth", label: a.t(lang, "qth"), value: profile.QTH, limit: 80},
		{name: "rig", label: a.t(lang, "rig"), value: profile.Rig, limit: 100},
	}
	if includePassword {
		fields = append(fields,
			formField{name: "new_password", label: a.t(lang, "new_password"), kind: fieldPassword},
			formField{name: "verify_password", label: a.t(lang, "verify_password"), kind: fieldPassword},
		)
	}
	return fields
}

func applyProfileValues(profile userProfile, values map[string]string) userProfile {
	profile.FullName = sanitizeSingleLineText(values["full_name"])
	profile.Email = sanitizeSingleLineText(values["email"])
	profile.Maidenhead = sanitizeSingleLineText(values["maidenhead"])
	profile.Language = sanitizeSingleLineText(values["language"])
	profile.EnableAPRS = values["enable_aprs"] == "true"
	profile.QTH = sanitizeSingleLineText(values["qth"])
	profile.Rig = sanitizeSingleLineText(values["rig"])
	return profile
}
