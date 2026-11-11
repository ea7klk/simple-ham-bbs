package main

import (
	"strings"
)

// The APRSPH net bot.
//
// APRSPH is a bot at the callsign APRSPH (not an ANSRVR group). It offers a set
// of commands, documented at https://aprsph.net/ under "How to join and use
// APRSPH". This file turns that command set into a menu, so a caller does not
// have to remember the syntax — and, more to the point, so nobody sends a
// message to the wrong destination again.
//
// Commands are APRS protocol, not prose: they are never translated. Only the
// labels, prompts and descriptions are.

const (
	aprsphCmdCQ       = "CQ"
	aprsphCmdNET      = "NET"
	aprsphCmdLIST     = "LIST"
	aprsphCmdLAST     = "LAST"
	aprsphCmdMINE     = "MINE"
	aprsphCmdSEARCH   = "SEARCH"
	aprsphCmdPath     = "?APRST"
	aprsphCmdPosition = "?APRSP"
	aprsphCmdStatus   = "?APRSS"
	aprsphCmdInbox    = "?APRSM"
	aprsphCmdCheckout = "U"
	aprsphCmdHelp     = "HELP"
	// aprsphCmdThursday selects the #APRSThursday log for LAST and MINE, and
	// also works with the count suffixes: "LAST10 THURS".
	aprsphCmdThursday = "THURS"
)

// aprsphCountSuffix maps a menu choice to the command's count form. The bot
// accepts a bare command (5 messages), or a "10"/"15" suffix.
func aprsphCountSuffix(choice string) string {
	switch choice {
	case "2":
		return "10"
	case "3":
		return "15"
	default:
		return ""
	}
}

// aprsphManageMenu is the entry point from the APRS menu.
func (a *app) aprsphManageMenu(callsign string, profile userProfile, lang string) {
	for {
		choice := a.runMenu(lang, a.t(lang, "aprsph_menu_title"), a.t(lang, "aprsph_menu_hint"), []option{
			{"1", a.t(lang, "aprsph_checkin")},
			{"2", a.t(lang, "aprsph_quiet_checkin")},
			{"3", a.t(lang, "aprsph_list")},
			{"4", a.t(lang, "aprsph_net_messages")},
			{"5", a.t(lang, "aprsph_my_messages")},
			{"6", a.t(lang, "aprsph_search")},
			{"7", a.t(lang, "aprsph_inbox")},
			{"8", a.t(lang, "aprsph_bot_info")},
			{"9", a.t(lang, "aprsph_checkout")},
			{"h", a.t(lang, "aprsph_help")},
			{"q", a.t(lang, "back_button")},
		})
		switch choice {
		case "1":
			a.aprsphCheckIn(callsign, profile, lang, false)
		case "2":
			a.aprsphCheckIn(callsign, profile, lang, true)
		case "3":
			a.aprsphSend(callsign, profile, lang, "aprsph_list", aprsphCmdLIST)
		case "4":
			a.aprsphMessageLog(callsign, profile, lang)
		case "5":
			a.aprsphMine(callsign, profile, lang)
		case "6":
			a.aprsphSearch(callsign, profile, lang)
		case "7":
			a.aprsphInbox(callsign, profile, lang)
		case "8":
			a.aprsphBotInfo(callsign, profile, lang)
		case "9":
			a.aprsphCheckout(callsign, profile, lang)
		case "h":
			a.aprsphHelp(lang)
		case "q":
			return
		}
	}
}

// aprsphSend dispatches a command that needs no parameters.
func (a *app) aprsphSend(callsign string, profile userProfile, lang, titleKey, command string) bool {
	title := a.t(lang, titleKey)
	if !a.requireAPRSEnabled(lang, title, profile) {
		return false
	}
	return a.dispatchAPRSCommand(callsign, lang, title, aprsBotAPRSPH, command)
}

// aprsphCheckIn is CQ (public) or NET (quiet): both take an optional or
// required message body after the command.
//
// CQ alerts everyone checked in for the day; NET logs the callsign without
// alerting them. NET may be sent with no text at all, which is why the two share
// this function but not its requirement.
func (a *app) aprsphCheckIn(callsign string, profile userProfile, lang string, quiet bool) {
	prefix := aprsphCmdCQ
	titleKey := "aprsph_checkin"
	if quiet {
		prefix = aprsphCmdNET
		titleKey = "aprsph_quiet_checkin"
	}
	title := a.t(lang, titleKey)
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}

	maxText := aprsCommandMaxBody - len([]rune(prefix)) - 1
	label := a.t(lang, "aprsph_checkin_text")
	if quiet {
		label = a.t(lang, "aprsph_checkin_text_optional")
	}
	action, values, ok := a.runForm(lang, title, []formField{
		{
			name: "text", label: label, kind: fieldTextArea, required: !quiet,
			validator:   func(v string) bool { return len([]rune(v)) <= maxText },
			invalidText: a.aprsCommandTooLongDetail(lang, maxText, maxText),
		},
	}, []string{"send", "cancel"})
	if !ok || action == "cancel" {
		return
	}
	kind := "checkin"
	if quiet {
		kind = "quiet"
	}
	command := aprsphCommandFor(kind, "", "", strings.TrimSpace(values["text"]), false)
	a.dispatchAPRSCommand(callsign, lang, title, aprsBotAPRSPH, command)
}

// aprsphMessageLog offers LAST and the #APRSThursday variant, each at 5, 10 or
// 15 messages.
func (a *app) aprsphMessageLog(callsign string, profile userProfile, lang string) {
	if !a.requireAPRSEnabled(lang, a.t(lang, "aprsph_net_messages"), profile) {
		return
	}
	choice := a.runMenu(lang, a.t(lang, "aprsph_net_messages"), a.t(lang, "aprsph_count_hint"), []option{
		{"1", a.t(lang, "aprsph_last_five")},
		{"2", a.t(lang, "aprsph_last_ten")},
		{"3", a.t(lang, "aprsph_last_fifteen")},
		{"4", a.t(lang, "aprsph_last_five_thurs")},
		{"5", a.t(lang, "aprsph_last_ten_thurs")},
		{"6", a.t(lang, "aprsph_last_fifteen_thurs")},
		{"q", a.t(lang, "back_button")},
	})
	if choice == "q" || choice == "" {
		return
	}
	thursday := choice == "4" || choice == "5" || choice == "6"
	command := aprsphCommandFor("last", choice, "", "", thursday)
	a.aprsphSend(callsign, profile, lang, "aprsph_net_messages", command)
}

// aprsphMine retrieves the caller's own recent messages, optionally for another
// callsign-SSID, and optionally from the #APRSThursday log.
func (a *app) aprsphMine(callsign string, profile userProfile, lang string) {
	title := a.t(lang, "aprsph_my_messages")
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}

	action, values, ok := a.runForm(lang, title, []formField{
		{
			name: "target", label: a.t(lang, "aprsph_callsign_optional"), limit: 12,
			normalizer:  normalizeCallsign,
			validator:   func(v string) bool { return aprsCallsignRE.MatchString(v) },
			invalidText: a.t(lang, "invalid_callsign"),
		},
		{
			name: "thursday", label: a.t(lang, "aprsph_thursday_only"), kind: fieldChoice,
			value: "false", choices: []option{{"false", a.t(lang, "no_label")}, {"true", a.t(lang, "yes_label")}},
		},
	}, []string{"send", "cancel"})
	if !ok || action == "cancel" {
		return
	}

	countChoice := a.runMenu(lang, title, a.t(lang, "aprsph_count_hint"), []option{
		{"1", a.t(lang, "aprsph_last_five")},
		{"2", a.t(lang, "aprsph_last_ten")},
		{"3", a.t(lang, "aprsph_last_fifteen")},
		{"q", a.t(lang, "back_button")},
	})
	if countChoice == "q" || countChoice == "" {
		return
	}

	command := aprsphCommandFor("mine", countChoice, strings.TrimSpace(values["target"]), "", values["thursday"] == "true")
	a.aprsphSend(callsign, profile, lang, title, command)
}

// aprsphSearch searches the month's messages for a word or phrase.
func (a *app) aprsphSearch(callsign string, profile userProfile, lang string) {
	title := a.t(lang, "aprsph_search")
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}

	// SEARCH10/SEARCH15 are three characters longer than SEARCH.
	maxText := aprsCommandMaxBody - len(aprsphCmdSEARCH) - 3 - 1
	action, values, ok := a.runForm(lang, title, []formField{
		{
			name: "phrase", label: a.t(lang, "aprsph_search_prompt"), required: true,
			validator:   func(v string) bool { return len([]rune(v)) <= maxText },
			invalidText: a.aprsCommandTooLongDetail(lang, maxText, maxText),
		},
	}, []string{"send", "cancel"})
	if !ok || action == "cancel" {
		return
	}

	countChoice := a.runMenu(lang, title, a.t(lang, "aprsph_count_hint"), []option{
		{"1", a.t(lang, "aprsph_last_five")},
		{"2", a.t(lang, "aprsph_last_ten")},
		{"3", a.t(lang, "aprsph_last_fifteen")},
		{"q", a.t(lang, "back_button")},
	})
	if countChoice == "q" || countChoice == "" {
		return
	}

	command := aprsphCommandFor("search", countChoice, "", strings.TrimSpace(values["phrase"]), false)
	a.aprsphSend(callsign, profile, lang, title, command)
}

// aprsphInbox retrieves the last messages sent to a callsign-SSID, via the
// bot's aprs.fi lookup.
func (a *app) aprsphInbox(callsign string, profile userProfile, lang string) {
	title := a.t(lang, "aprsph_inbox")
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}
	action, values, ok := a.runForm(lang, title, []formField{
		{
			name: "target", label: a.t(lang, "aprsph_callsign_optional"), limit: 12,
			normalizer:  normalizeCallsign,
			validator:   func(v string) bool { return aprsCallsignRE.MatchString(v) },
			invalidText: a.t(lang, "invalid_callsign"),
		},
	}, []string{"send", "cancel"})
	if !ok || action == "cancel" {
		return
	}
	command := aprsphCommandFor("inbox", "", strings.TrimSpace(values["target"]), "", false)
	a.aprsphSend(callsign, profile, lang, title, command)
}

// aprsphBotInfo offers the three bot queries that need no parameters.
func (a *app) aprsphBotInfo(callsign string, profile userProfile, lang string) {
	title := a.t(lang, "aprsph_bot_info")
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}
	choice := a.runMenu(lang, title, a.t(lang, "aprsph_bot_info_hint"), []option{
		{"1", a.t(lang, "aprsph_path")},
		{"2", a.t(lang, "aprsph_position")},
		{"3", a.t(lang, "aprsph_status")},
		{"q", a.t(lang, "back_button")},
	})
	switch choice {
	case "1":
		a.aprsphSend(callsign, profile, lang, "aprsph_path", aprsphCmdPath)
	case "2":
		a.aprsphSend(callsign, profile, lang, "aprsph_position", aprsphCmdPosition)
	case "3":
		a.aprsphSend(callsign, profile, lang, "aprsph_status", aprsphCmdStatus)
	}
}

// aprsphCheckout leaves the day's net, optionally with a parting message that
// is recorded in the log.
func (a *app) aprsphCheckout(callsign string, profile userProfile, lang string) {
	title := a.t(lang, "aprsph_checkout")
	if !a.requireAPRSEnabled(lang, title, profile) {
		return
	}
	maxText := aprsCommandMaxBody - len(aprsphCmdCheckout) - 1
	action, values, ok := a.runForm(lang, title, []formField{
		{
			name: "text", label: a.t(lang, "aprsph_checkout_text"), kind: fieldTextArea,
			validator:   func(v string) bool { return len([]rune(v)) <= maxText },
			invalidText: a.aprsCommandTooLongDetail(lang, maxText, maxText),
		},
	}, []string{"send", "cancel"})
	if !ok || action == "cancel" {
		return
	}
	command := aprsphCommandFor("checkout", "", "", strings.TrimSpace(values["text"]), false)
	a.aprsphSend(callsign, profile, lang, title, command)
}

// aprsphHelp shows the command summary, so a caller can see what the bot offers
// without leaving the BBS.
//
// This is a description rather than a generated list: the authoritative source
// is the net's own page, and the identifiers below are APRS protocol keywords
// that must not be translated.
func (a *app) aprsphHelp(lang string) {
	lines := a.tList(lang, "aprsph_help_lines")
	if len(lines) == 0 {
		lines = []string{"See https://aprsph.net/ for the full command list."}
	}
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, []string{dimStyle.Render(singleLineAPRSDetail(line))})
	}
	a.showInfoActions(lang, a.t(lang, "aprsph_help"), rows, []option{{"q", a.t(lang, "back_button")}})
}

// aprsphCommandFor is the command a menu entry produces, exposed for tests so
// the mapping can be asserted without a TUI.
func aprsphCommandFor(kind, countChoice, target, phrase string, thursday bool) string {
	switch kind {
	case "last":
		// The LAST submenu numbers six entries: 1-3 for the net log and 4-6 for
		// the #APRSThursday log, each at 5, 10 and 15 messages.
		command := aprsphCmdLAST
		switch countChoice {
		case "2", "5":
			command += "10"
		case "3", "6":
			command += "15"
		}
		if thursday {
			command += " " + aprsphCmdThursday
		}
		return command
	case "mine":
		command := aprsphCmdMINE + aprsphCountSuffix(countChoice)
		if thursday {
			command += " " + aprsphCmdThursday
		} else if target != "" {
			command += " " + target
		}
		return command
	case "search":
		return aprsphCmdSEARCH + aprsphCountSuffix(countChoice) + " " + phrase
	case "inbox":
		if target == "" {
			return aprsphCmdInbox
		}
		return aprsphCmdInbox + " " + target
	case "checkin":
		if phrase == "" {
			return aprsphCmdCQ
		}
		return aprsphCmdCQ + " " + phrase
	case "quiet":
		if phrase == "" {
			return aprsphCmdNET
		}
		return aprsphCmdNET + " " + phrase
	case "checkout":
		if phrase == "" {
			return aprsphCmdCheckout
		}
		return aprsphCmdCheckout + " " + phrase
	case "list":
		return aprsphCmdLIST
	case "path":
		return aprsphCmdPath
	case "position":
		return aprsphCmdPosition
	case "status":
		return aprsphCmdStatus
	case "help":
		return aprsphCmdHelp
	}
	return ""
}

// aprsphCommandFits reports whether a command fits in one APRS message after the
// message ID is allowed for.
func aprsphCommandFits(command string) bool {
	return len([]rune(command)) <= aprsCommandMaxBody
}
