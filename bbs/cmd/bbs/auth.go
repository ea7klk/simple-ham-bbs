package main

import (
	"errors"
	"strings"
)

var errLoginCancelled = errors.New("login cancelled")

func (a *app) authenticate() (string, userProfile, error) {
	users, err := a.loadUsers()
	if err != nil {
		return "", userProfile{}, err
	}
	if changed := a.applyConfiguredSysops(users); changed {
		_ = a.saveUsers(users)
	}
	failedAttempts := map[string]int{}
	for {
		var header strings.Builder
		for _, code := range languageOrder {
			header.WriteString(languages[code] + ": " + truncateText(strings.Join(a.tList(code, "login_info"), " "), 62) + "\n")
		}
		loginTitle := "Login\n" + strings.TrimSpace(header.String())
		_, values, ok := a.runForm("en", loginTitle, []formField{
			{
				name: "callsign", label: a.t("en", "callsign_prompt"), required: true, limit: 16,
				normalizer: normalizeCallsign, validator: func(v string) bool { return callsignRE.MatchString(v) }, invalidText: a.t("en", "invalid_callsign"),
			},
			{name: "password", label: a.t("en", "password"), kind: fieldPassword},
		}, []string{"login"})
		if !ok {
			return "", userProfile{}, errLoginCancelled
		}
		callsign := normalizeCallsign(values["callsign"])
		password := values["password"]
		profile, exists := users[callsign]
		if !exists {
			profile, ok = a.register(callsign, userProfile{Language: "en"}, true)
			if !ok {
				return "", userProfile{}, errLoginCancelled
			}
			users[callsign] = profile
			_ = a.saveUsers(users)
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
			_ = a.saveUsers(users)
			return callsign, profile, nil
		}
		if verifyPassword(password, profile.PasswordHash) {
			if !profileComplete(profile) {
				profile, ok = a.register(callsign, profile, false)
				if !ok {
					return "", userProfile{}, errLoginCancelled
				}
			}
			profile.LastSeen = now()
			users[callsign] = profile
			_ = a.saveUsers(users)
			return callsign, profile, nil
		}
		failedAttempts[callsign]++
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
	profile.IsSysop = profile.IsSysop || a.cfg.sysops[callsign]
	for {
		fields := a.profileFields(profile, true, forcePassword || profile.PasswordHash == "")
		_, values, ok := a.runForm(profile.Language, a.t(profile.Language, "registration_form_title")+" - "+callsign, fields, []string{"save", "cancel"})
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
	profile = applyProfileValues(profile, values)
	if values["new_password"] != "" {
		profile.PasswordHash = hashPassword(values["new_password"])
	}
	profile.LastSeen = now()
	users[callsign] = profile
	_ = a.saveUsers(users)
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
	profile.FullName = values["full_name"]
	profile.Email = values["email"]
	profile.Maidenhead = values["maidenhead"]
	profile.Language = values["language"]
	profile.EnableAPRS = values["enable_aprs"] == "true"
	profile.QTH = values["qth"]
	profile.Rig = values["rig"]
	return profile
}
