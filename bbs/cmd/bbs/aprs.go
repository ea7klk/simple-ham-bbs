package main

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	aprsStatusSent   = "sent"
	aprsStatusFailed = "failed"
	aprsBeaconText   = "HamNet BBS"

	aprsListLabelWidth       = panelContentWidth - (menuOptionColumnWidth + 4) // Option plus row prefix.
	aprsCallsignColumnWidth  = 13
	aprsTimestampColumnWidth = 20
	aprsListGapWidth         = 2
	aprsReceivedTextWidth    = aprsListLabelWidth - aprsCallsignColumnWidth - aprsTimestampColumnWidth - 2*aprsListGapWidth
	aprsSentTextWidth        = aprsListLabelWidth - 1 - aprsTimestampColumnWidth - aprsCallsignColumnWidth - 3*aprsListGapWidth
)

var netDialTimeout = net.DialTimeout

func (a *app) aprsMenu(callsign string, profile userProfile, lang string) userProfile {
	users, _ := a.loadUsers()
	profile = users[callsign]
	for {
		header := fmt.Sprintf("%s: %s\n%s", a.t(lang, "aprs_status"), boolString(profile.EnableAPRS), a.t(lang, "aprs_ssid_info"))
		choice := a.runMenu(lang, a.t(lang, "menu_aprs"), header, []option{
			{"1", a.t(lang, "aprs_received_messages")},
			{"2", a.t(lang, "aprs_sent_messages")},
			{"3", a.t(lang, "aprs_send_message")},
			{"4", a.t(lang, "aprs_join_aprs_thursday")},
			{"5", a.t(lang, "aprs_leave_aprs_thursday")},
			{"6", a.t(lang, "aprs_keepalive_aprs_thursday")},
			{"7", a.t(lang, "aprs_join_aprsph")},
			{"8", a.t(lang, "aprs_set_enabled")},
			{"9", a.t(lang, "aprs_bulletins_menu")},
			{"q", a.t(lang, "menu_quit")},
		})
		switch choice {
		case "1":
			a.aprsReceivedSection(callsign, profile, lang)
		case "2":
			a.aprsSentSection(callsign, profile, lang)
		case "3":
			a.sendAPRS(callsign, profile, lang)
		case "4":
			a.joinAPRSThursday(callsign, profile, lang)
		case "5":
			a.leaveAPRSThursday(callsign, profile, lang)
		case "6":
			a.keepaliveAPRSThursday(callsign, profile, lang)
		case "7":
			a.aprsphManageMenu(callsign, profile, lang)
		case "9":
			a.showAPRSBulletinCategories(lang)
		case "8":
			_, values, ok := a.runForm(lang, a.t(lang, "aprs_enable_title"), []formField{{name: "enable_aprs", label: a.t(lang, "enable_aprs"), kind: fieldChoice, value: boolString(profile.EnableAPRS), choices: []option{{"false", "false"}, {"true", "true"}}}}, []string{"save", "cancel"})
			if ok {
				profile.EnableAPRS = values["enable_aprs"] == "true"
				profile.LastSeen = now()
				if err := a.saveUser(callsign, profile); err != nil {
					a.showMutationError(lang, a.t(lang, "aprs_enable_title"), err)
				}
			}
		case "q":
			return profile
		}
	}
}

// aprsReceivedSection offers the three sections of the received history, with
// their counts, and opens the one chosen.
func (a *app) aprsReceivedSection(callsign string, profile userProfile, lang string) {
	if !a.requireAPRSEnabled(lang, a.t(lang, "aprs_received_messages"), profile) {
		return
	}
	counts := countReceivedScopes(a.trimReceived(callsign))
	scope, ok := a.aprsSectionMenu(lang, "aprs_received_messages", counts)
	if !ok {
		return
	}
	a.showReceivedAPRS(callsign, profile, lang, scope)
}

// aprsSentSection is the same for the sent history.
func (a *app) aprsSentSection(callsign string, profile userProfile, lang string) {
	if !a.requireAPRSEnabled(lang, a.t(lang, "aprs_sent_messages"), profile) {
		return
	}
	counts := countSentScopes(a.trimSent(callsign))
	scope, ok := a.aprsSectionMenu(lang, "aprs_sent_messages", counts)
	if !ok {
		return
	}
	a.showSentAPRS(callsign, profile, lang, scope)
}

func (a *app) showReceivedAPRS(callsign string, profile userProfile, lang string, scope aprsScope) {
	for {
		history := reverseReceived(filterReceivedByScope(a.trimReceived(callsign), scope))
		title := fmt.Sprintf("%d %s (%s)", len(history), a.t(lang, "aprs_received_messages"), a.t(lang, scope.labelKey()))
		if !profile.EnableAPRS {
			a.showInfo(lang, title, [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return
		}
		if len(history) == 0 {
			a.showInfoActions(lang, title, [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_no_received_messages")}}, []option{{"q", a.t(lang, "back_button")}})
			return
		}
		opts := []option{}
		for i, item := range history {
			opts = append(opts, option{strconv.Itoa(i + 1), a.receivedAPRSListLabel(item)})
		}
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		choice := a.runMenu(lang, title, a.t(lang, "aprs_latest_received"), opts)
		if choice == "q" {
			return
		}
		idx, _ := strconv.Atoi(choice)
		idx--
		if idx < 0 || idx >= len(history) {
			continue
		}
		action := a.showInfoActions(lang, a.t(lang, "aprs_received_message_detail"), a.aprsReceivedDetailRows(lang, history[idx]), []option{{"q", a.t(lang, "back_button")}, {"r", a.t(lang, "reply_button")}, {"d", a.t(lang, "delete_button")}})
		switch action {
		case "d":
			if a.confirmDelete(lang, a.t(lang, "confirm_delete_aprs_message")) {
				if err := a.deleteReceivedRecord(history[idx].ID); err != nil {
					a.showMutationError(lang, a.t(lang, "aprs_received_message_detail"), err)
					continue
				}
				a.logBBSAction(callsign, "aprs_received_delete", "from=%q at=%q", history[idx].From, history[idx].At)
			}
		case "r":
			a.sendAPRSForm(callsign, profile, lang, normalizeAPRSCallsign(history[idx].From))
		}
	}
}

// aprsSsid0Warning is the line shown above every APRS send form.
//
// Every message leaves from the base callsign with SSID 0 — see aprsSSID0 — so
// an operator who addresses EA7KLK-15 should not expect the traffic to appear to
// come from EA7KLK-15. Stating it at the point of sending is the only place it
// cannot be missed. The callsign is interpolated so the line names the operator's
// own station rather than a generic example.
func (a *app) aprsSsid0Warning(lang, callsign string) []string {
	const key = "aprs_ssid0_warning"
	source := aprsSSID0(callsign)
	tmpl := a.t(lang, key)
	if tmpl == key {
		// A missing key would otherwise reach Sprintf and render as
		// "aprs_ssid0_warning%!(EXTRA string=…)", which is worse than English.
		tmpl = "Every message you send leaves from %s, whatever SSID you address."
	}
	return []string{fmt.Sprintf(tmpl, source)}
}

func (a *app) sendAPRS(callsign string, profile userProfile, lang string) {
	// A completed send returns to the APRS menu. Retry handling stays inside
	// sendAPRSForm, so this entry point must not reopen the send form.
	_ = a.sendAPRSForm(callsign, profile, lang, "")
}

func (a *app) sendAPRSForm(callsign string, profile userProfile, lang, destination string) bool {
	destCall, destSSID := splitAPRSDestination(destination)
	for {
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_send_message"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return false
		}
		action, values, ok := a.runFormStyled(lang, a.t(lang, "aprs_send_message"), []formField{
			{name: "destination", label: a.t(lang, "aprs_destination_callsign"), value: destCall, required: true, limit: 10, width: 10, sameLine: true, normalizer: normalizeAPRSCallsign, validator: validAPRSBaseCallsign, invalidText: a.t(lang, "aprs_invalid_destination")},
			{name: "destination_ssid", label: a.t(lang, "aprs_destination_ssid"), value: destSSID, limit: 2, width: 2, sameLine: true, normalizer: normalizeAPRSSSID, validator: validAPRSSSID, invalidText: a.t(lang, "aprs_invalid_ssid")},
			{name: "text", label: a.t(lang, "aprs_text"), kind: fieldTextArea, limit: 2000},
		}, []string{"send", "cancel"}, formStyle{Intro: a.aprsSsid0Warning(lang, callsign)})
		if !ok || action == "cancel" {
			return false
		}
		destination = composeAPRSDestination(values["destination"], values["destination_ssid"])
		destCall, destSSID = splitAPRSDestination(destination)
		a.showSendingAPRS(lang, destination)
		sent, okSend := a.sendAPRSMessage(callsign, destination, values["text"], lang)
		if !okSend {
			detail := ""
			if len(sent.Parts) > 0 {
				detail = sent.Parts[len(sent.Parts)-1].Detail
			}
			if detail == "" {
				detail = sent.Status
			}
			action := a.showInfoActions(lang, a.t(lang, "aprs_send_failed"), [][]string{{detail}, {a.t(lang, "aprs_retry_prompt")}}, []option{{"r", a.t(lang, "retry_button")}, {"q", a.t(lang, "cancel_button")}})
			if action == "r" {
				continue
			}
			return false
		}
		_ = a.addSent(callsign, sent)
		a.logBBSAction(callsign, "aprs_send", "to=%q parts=%d", sent.To, len(sent.Parts))
		a.showInfoActions(lang, a.t(lang, "aprs_send_success"), a.aprsSentResultRows(lang, sent), []option{{"o", a.t(lang, "ok_button")}})
		return true
	}
}

// destination constants for the two APRS bots this menu talks to.
//
// APRSPH is a bot at the callsign APRSPH, not a group: it is addressed directly
// and answers every message. ANSRVR is the group server, addressed with a
// command in the text ("CQ <group>", "U <group>", "K <group>").
const (
	aprsBotAPRSPH = "APRSPH"
	aprsBotANSRVR = "ANSRVR"
	aprsGroupHOTG = "HOTG"
	aprsCommandCQ = "CQ"
	aprsCommandU  = "U"
	aprsCommandK  = "K"
	aprsHOTGJoin  = aprsCommandCQ + " " + aprsGroupHOTG
	aprsHOTGLeave = aprsCommandU + " " + aprsGroupHOTG
	aprsHOTGKeep  = aprsCommandK + " " + aprsGroupHOTG
	aprsAPRSPHCQ  = aprsCommandCQ
)

// joinAPRSThursday checks in to the #APRSThursday net: "CQ HOTG <text>" to the
// ANSRVR group server.
func (a *app) joinAPRSThursday(callsign string, profile userProfile, lang string) {
	if time.Now().UTC().Weekday() != time.Thursday {
		a.showInfoActions(lang, a.t(lang, "aprs_join_aprs_thursday"),
			[][]string{{a.t(lang, "aprs_not_thursday_warning")}},
			[]option{{"q", a.t(lang, "back_button")}})
		return
	}
	a.sendAPRSBotCommand(callsign, profile, lang, "aprs_join_aprs_thursday", aprsBotANSRVR, aprsHOTGJoin, true)
}

// leaveAPRSThursday unsubscribes from the HOTG group: "U HOTG" to ANSRVR.
//
// #APRSThursday asks participants to leave when they stop, so their local RF
// network is not kept busy by the group's traffic. Not gated on the weekday: you
// may want to leave any day.
func (a *app) leaveAPRSThursday(callsign string, profile userProfile, lang string) {
	if !profile.EnableAPRS {
		a.showInfo(lang, a.t(lang, "aprs_leave_aprs_thursday"), [][]string{{a.t(lang, "aprs_enable_required")}})
		return
	}
	if !a.confirmDelete(lang, a.t(lang, "aprs_leave_confirm")) {
		return
	}
	a.sendAPRSBotCommand(callsign, profile, lang, "aprs_leave_aprs_thursday", aprsBotANSRVR, aprsHOTGLeave, false)
}

// keepaliveAPRSThursday extends the HOTG subscription: "K HOTG" to ANSRVR.
func (a *app) keepaliveAPRSThursday(callsign string, profile userProfile, lang string) {
	a.sendAPRSBotCommand(callsign, profile, lang, "aprs_keepalive_aprs_thursday", aprsBotANSRVR, aprsHOTGKeep, false)
}

// sendAPRSBotCommand sends one bot command, optionally with the user's text
// appended to a fixed prefix.
//
// The command is sent as a single APRS message: splitting would put the command
// into part one with a "[1/2] " prefix and the rest into part two, so neither
// part would be a valid command.
// requireAPRSEnabled shows the enable prompt and reports whether the account may
// send. Every bot path needs this check and the message is always the same.
func (a *app) requireAPRSEnabled(lang, title string, profile userProfile) bool {
	if profile.EnableAPRS {
		return true
	}
	a.showInfo(lang, title, [][]string{
		{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)},
		{a.t(lang, "aprs_ssid_info")},
		{a.t(lang, "aprs_enable_required")},
	})
	return false
}

// dispatchAPRSCommand sends one prepared bot command and reports the result.
//
// Shared by the ANSRVR check-ins and the APRSPH command set, so both show the
// same failure detail and record the same history row.
func (a *app) dispatchAPRSCommand(callsign string, lang, title, destination, command string) bool {
	// Say where it is going: the two bots are easy to confuse, and the
	// difference is exactly what used to break this.
	a.showSendingAPRS(lang, destination)
	sent, ok := a.sendAPRSCommand(callsign, destination, command, lang)
	if !ok {
		detail := ""
		if len(sent.Parts) > 0 {
			detail = sent.Parts[len(sent.Parts)-1].Detail
		}
		if detail == "" {
			detail = sent.Status
		}
		a.showInfoActions(lang, a.t(lang, "aprs_send_failed"),
			[][]string{{detail}, {a.t(lang, "aprs_retry_prompt")}},
			[]option{{"o", a.t(lang, "ok_button")}})
		return false
	}
	_ = a.addSent(callsign, sent)
	a.logBBSAction(callsign, "aprs_send", "to=%q parts=%d command=%q", sent.To, len(sent.Parts), command)
	a.showInfoActions(lang, a.t(lang, "aprs_send_success"), a.aprsSentResultRows(lang, sent), []option{{"o", a.t(lang, "ok_button")}})
	return true
}

func (a *app) sendAPRSBotCommand(callsign string, profile userProfile, lang, titleKey, destination, prefix string, askText bool) bool {
	title := a.t(lang, titleKey)
	for {
		if !a.requireAPRSEnabled(lang, title, profile) {
			return false
		}

		text := prefix
		if askText {
			// The prefix and the trailing message ID both consume space in the
			// 67-character field; what is left is the user's budget.
			maxText := aprsCommandMaxBody - len([]rune(prefix)) - 1
			if maxText < 10 {
				maxText = 10
			}
			invalidText := a.aprsCommandTooLongDetail(lang, maxText, maxText)
			action, values, ok := a.runFormStyled(lang, title, []formField{
				{
					name: "text", label: a.t(lang, "aprs_ansrvr_text"), kind: fieldTextArea, required: true,
					validator:   func(v string) bool { return len([]rune(v)) <= maxText },
					invalidText: invalidText,
				},
			}, []string{"send", "cancel"}, formStyle{Intro: a.aprsSsid0Warning(lang, callsign)})
			if !ok || action == "cancel" {
				return false
			}
			text = prefix + " " + strings.TrimSpace(values["text"])
		}

		if a.dispatchAPRSCommand(callsign, lang, title, destination, text) {
			return true
		}
		// Offer a retry rather than dropping the user back to the menu.
		action := a.showInfoActions(lang, a.t(lang, "aprs_send_failed"),
			[][]string{{a.t(lang, "aprs_retry_prompt")}},
			[]option{{"r", a.t(lang, "retry_button")}, {"q", a.t(lang, "cancel_button")}})
		if action != "r" {
			return false
		}
	}
}

func (a *app) showSentAPRS(callsign string, profile userProfile, lang string, scope aprsScope) {
	for {
		history := reverseSent(filterSentByScope(a.trimSent(callsign), scope))
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_sent_messages"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return
		}
		if len(history) == 0 {
			a.showInfoActions(lang, a.t(lang, "aprs_sent_messages"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_no_sent_messages")}}, []option{{"q", a.t(lang, "back_button")}})
			return
		}
		opts := []option{}
		for i, item := range history {
			opts = append(opts, option{strconv.Itoa(i + 1), a.sentAPRSListLabel(item)})
		}
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		choice := a.runMenu(lang, fmt.Sprintf("%d %s (%s)", len(history), a.t(lang, "aprs_sent_messages"), a.t(lang, scope.labelKey())), a.t(lang, "aprs_latest_sent"), opts)
		if choice == "q" {
			return
		}
		idx, _ := strconv.Atoi(choice)
		idx--
		if idx < 0 || idx >= len(history) {
			continue
		}
		action := a.showInfoActions(lang, a.t(lang, "aprs_sent_message_detail"), a.aprsSentDetailRows(lang, history[idx]), []option{{"q", a.t(lang, "back_button")}, {"d", a.t(lang, "delete_button")}})
		if action == "d" {
			if a.confirmDelete(lang, a.t(lang, "confirm_delete_aprs_message")) {
				if err := a.deleteSentRecord(history[idx].ID); err != nil {
					a.showMutationError(lang, a.t(lang, "aprs_sent_message_detail"), err)
					continue
				}
				a.logBBSAction(callsign, "aprs_sent_delete", "to=%q at=%q", history[idx].To, history[idx].At)
			}
		}
	}
}

func (a *app) aprsSentRows(lang string, enabled bool, history []sentAPRS) [][]string {
	rows := [][]string{{a.t(lang, "aprs_status"), boolString(enabled)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_latest_sent")}}
	if len(history) == 0 {
		return append(rows, []string{a.t(lang, "aprs_no_sent_messages")})
	}
	for _, item := range reverseSent(history) {
		rows = append(rows, []string{item.At, a.sentAPRSSummary(lang, item)})
	}
	return rows
}

func (a *app) aprsReceivedRows(lang string, enabled bool, history []receivedAPRS) [][]string {
	rows := [][]string{{a.t(lang, "aprs_status"), boolString(enabled)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_latest_received")}}
	if len(history) == 0 {
		return append(rows, []string{a.t(lang, "aprs_no_received_messages")})
	}
	for _, item := range reverseReceived(history) {
		rows = append(rows, []string{item.At, a.receivedAPRSSummary(item)})
	}
	return rows
}

func (a *app) receivedAPRSSummary(item receivedAPRS) string {
	return fmt.Sprintf("%s -> %s / %s", item.From, item.To, singleLineAPRSDetail(stripAPRSMessageID(item.Text)))
}

func (a *app) receivedAPRSListLabel(item receivedAPRS) string {
	return paddedCell(item.From, aprsCallsignColumnWidth) +
		strings.Repeat(" ", aprsListGapWidth) +
		paddedCell(item.At, aprsTimestampColumnWidth) +
		strings.Repeat(" ", aprsListGapWidth) +
		paddedCell(singleLineAPRSDetail(stripAPRSMessageID(item.Text)), aprsReceivedTextWidth)
}

func (a *app) sentAPRSSummary(lang string, item sentAPRS) string {
	status := a.aprsStatusLabel(lang, item.Status)
	return fmt.Sprintf("%s -> %s / %s / %d %s / %s", item.From, item.To, status, len(item.Parts), a.t(lang, "aprs_parts"), singleLineAPRSDetail(item.Text))
}

func (a *app) sentAPRSListLabel(item sentAPRS) string {
	return paddedCell(sentAckIcon(item), 1) +
		strings.Repeat(" ", aprsListGapWidth) +
		paddedCell(item.At, aprsTimestampColumnWidth) +
		strings.Repeat(" ", aprsListGapWidth) +
		paddedCell(item.To, aprsCallsignColumnWidth) +
		strings.Repeat(" ", aprsListGapWidth) +
		paddedCell(singleLineAPRSDetail(item.Text), aprsSentTextWidth)
}

func sentAckIcon(item sentAPRS) string {
	if normalizeAPRSStatus(item.Status) != aprsStatusSent {
		return ""
	}
	if len(item.Parts) == 0 {
		if item.Acked {
			return "✓"
		}
		return ""
	}
	acked := 0
	for _, part := range item.Parts {
		if part.Acked {
			acked++
		}
	}
	if acked == len(item.Parts) {
		return "✓"
	}
	if len(item.Parts) > 1 {
		return "?"
	}
	return ""
}

func (a *app) aprsSentResultRows(lang string, item sentAPRS) [][]string {
	rows := [][]string{
		{a.t(lang, "from"), item.From},
		{a.t(lang, "aprs_destination_callsign"), item.To},
		{a.t(lang, "aprs_parts"), fmt.Sprintf("%d", len(item.Parts))},
	}
	for _, part := range item.Parts {
		rows = append(rows, []string{fmt.Sprintf("%d", part.Number), a.aprsStatusLabel(lang, part.Status) + " / " + singleLineAPRSDetail(part.Text)})
	}
	return rows
}

func (a *app) aprsSentDetailRows(lang string, item sentAPRS) [][]string {
	partStatuses := []string{}
	for _, part := range item.Parts {
		status := fmt.Sprintf("%d:%s", part.Number, a.aprsStatusLabel(lang, part.Status))
		if part.Detail != "" {
			status += " (" + part.Detail + ")"
		}
		partStatuses = append(partStatuses, status)
	}
	rows := [][]string{
		{a.t(lang, "from"), item.From},
		{a.t(lang, "aprs_destination_callsign"), item.To},
		{a.t(lang, "at"), item.At},
		{a.t(lang, "status"), a.aprsStatusLabel(lang, item.Status)},
		{a.t(lang, "aprs_acknowledged"), boolString(item.Acked)},
		{a.t(lang, "aprs_parts"), fmt.Sprintf("%d", len(item.Parts))},
		{a.t(lang, "aprs_text"), singleLineAPRSDetail(item.Text)},
	}
	if len(partStatuses) > 0 {
		rows = append(rows, []string{a.t(lang, "aprs_part_statuses"), strings.Join(partStatuses, "  ")})
	}
	return rows
}

func (a *app) aprsReceivedDetailRows(lang string, item receivedAPRS) [][]string {
	rows := [][]string{
		{a.t(lang, "from"), item.From},
		{a.t(lang, "aprs_destination_callsign"), item.To},
		{a.t(lang, "at"), item.At},
		{a.t(lang, "aprs_text"), singleLineAPRSDetail(stripAPRSMessageID(item.Text))},
	}
	if item.Raw != "" {
		rows = append(rows,
			[]string{""},
			[]string{strings.Repeat("-", panelContentWidth)},
			[]string{""},
			[]string{a.t(lang, "aprs_raw_packet"), singleLineAPRSDetail(item.Raw)},
		)
	}
	return rows
}

// singleLineAPRSDetail renders APRS text on one line and strips terminal
// controls. The rules live in sanitizeTerminalText so every user-facing surface
// shares one implementation.
func singleLineAPRSDetail(text string) string {
	return sanitizeSingleLineText(text)
}

func (a *app) showSendingAPRS(lang, destination string) {
	clearScreen()
	body := a.banner(lang) +
		titleStyle.Render(a.t(lang, "aprs_sending_message")) + "\n\n" +
		a.t(lang, "aprs_destination_callsign") + ": " + normalizeAPRSCallsign(destination) + "\n\n" +
		dimStyle.Render(a.t(lang, "aprs_please_wait"))
	fmt.Print(panelStyle.Render(body))
}

func (a *app) trimSent(callsign string) []sentAPRS {
	key := normalizeCallsign(callsign)
	_ = a.trimSentRows(key)
	return a.loadSentHistory(key)
}

func (a *app) trimReceived(callsign string) []receivedAPRS {
	key := normalizeCallsign(callsign)
	_ = a.trimReceivedRows(key)
	return a.loadReceivedHistory(key)
}

func (a *app) addSent(callsign string, sent sentAPRS) []sentAPRS {
	history, err := a.addSentRecord(normalizeCallsign(callsign), sent)
	if err != nil {
		return a.trimSent(callsign)
	}
	return history
}

func (a *app) loadSentHistory(callsign string) []sentAPRS {
	rows := []dbAPRSSent{}
	if err := a.db.Preload("Parts", func(db *gorm.DB) *gorm.DB { return db.Order("number") }).Where("user_callsign = ?", normalizeCallsign(callsign)).Order("position, id").Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]sentAPRS, 0, len(rows))
	for _, row := range rows {
		item := sentAPRS{ID: row.ID, At: row.At, From: row.From, To: row.To, Text: singleLineAPRSDetail(row.Text), Status: normalizeAPRSStatus(row.Status), Acked: row.Acked, Passcode: row.Passcode}
		for _, part := range row.Parts {
			item.Parts = append(item.Parts, sentAPRSPart{Number: part.Number, Text: singleLineAPRSDetail(part.Text), Status: normalizeAPRSStatus(part.Status), Detail: singleLineAPRSDetail(part.Detail), MessageID: part.MessageID, Acked: part.Acked})
		}
		out = append(out, item)
	}
	return out
}

func (a *app) deleteSentRecord(id uint) error {
	if id == 0 {
		return nil
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&dbAPRSSentPart{}, "sent_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&dbAPRSSent{}, id).Error
	})
}

func (a *app) addSentRecord(callsign string, sent sentAPRS) ([]sentAPRS, error) {
	callsign = normalizeCallsign(callsign)
	sent.Text = singleLineAPRSDetail(sent.Text)
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var maxPos int
		_ = tx.Model(&dbAPRSSent{}).Where("user_callsign = ?", callsign).Select("COALESCE(MAX(position), -1)").Scan(&maxPos).Error
		row := dbAPRSSent{UserCallsign: callsign, Position: maxPos + 1, At: sent.At, From: sent.From, To: sent.To, Text: sent.Text, Status: normalizeAPRSStatus(sent.Status), Acked: sent.Acked, Passcode: sent.Passcode}
		if row.At == "" {
			row.At = now()
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		for i, part := range sent.Parts {
			number := part.Number
			if number == 0 {
				number = i + 1
			}
			if err := tx.Create(&dbAPRSSentPart{SentID: row.ID, Number: number, Text: singleLineAPRSDetail(part.Text), Status: normalizeAPRSStatus(part.Status), Detail: singleLineAPRSDetail(part.Detail), MessageID: part.MessageID, Acked: part.Acked}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := a.trimSentRows(callsign); err != nil {
		return nil, err
	}
	return a.loadSentHistory(callsign), nil
}

func (a *app) trimSentRows(callsign string) error {
	var rows []dbAPRSSent
	if err := a.db.Where("user_callsign = ?", normalizeCallsign(callsign)).Order("position DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}
	for i, row := range rows {
		if i >= sentHistoryLimit {
			if err := a.db.Delete(&dbAPRSSentPart{}, "sent_id = ?", row.ID).Error; err != nil {
				return err
			}
			if err := a.db.Delete(&dbAPRSSent{}, row.ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) markSentAPRSAck(from, to, messageID string) (bool, error) {
	messageID = normalizeAPRSMessageID(messageID)
	if messageID == "" {
		return false, nil
	}
	user := aprsRecipientKey(to)
	from = normalizeAPRSCallsign(from)
	to = normalizeAPRSCallsign(to)
	matched := false
	err := a.db.Transaction(func(tx *gorm.DB) error {
		rows := []dbAPRSSent{}
		if err := tx.Preload("Parts", func(db *gorm.DB) *gorm.DB { return db.Order("number") }).
			Where("user_callsign = ? AND `from` = ? AND `to` = ?", user, to, from).
			Order("position DESC, id DESC").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			for _, part := range row.Parts {
				if normalizeAPRSMessageID(part.MessageID) != messageID {
					continue
				}
				matched = true
				if err := tx.Model(&dbAPRSSentPart{}).Where("id = ?", part.ID).Updates(map[string]any{"acked": true}).Error; err != nil {
					return err
				}
				allAcked := true
				for _, candidate := range row.Parts {
					if candidate.ID == part.ID {
						candidate.Acked = true
					}
					if !candidate.Acked {
						allAcked = false
						break
					}
				}
				if allAcked {
					if err := tx.Model(&dbAPRSSent{}).Where("id = ?", row.ID).Update("acked", true).Error; err != nil {
						return err
					}
				}
				return nil
			}
		}
		return nil
	})
	return matched, err
}

func (a *app) loadReceivedHistory(callsign string) []receivedAPRS {
	rows := []dbAPRSReceived{}
	if err := a.db.Where("user_callsign = ?", normalizeCallsign(callsign)).Order("position, id").Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]receivedAPRS, 0, len(rows))
	for _, row := range rows {
		out = append(out, receivedAPRS{ID: row.ID, At: row.At, From: row.From, To: row.To, Text: singleLineAPRSDetail(stripAPRSMessageID(row.Text)), Raw: row.Raw})
	}
	return out
}

func (a *app) deleteReceivedRecord(id uint) error {
	if id == 0 {
		return nil
	}
	return a.db.Delete(&dbAPRSReceived{}, id).Error
}

func (a *app) addReceivedRecord(callsign string, msg receivedAPRS) ([]receivedAPRS, error) {
	callsign = normalizeCallsign(callsign)
	msg.Text = singleLineAPRSDetail(stripAPRSMessageID(msg.Text))
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if msg.Raw != "" {
			var count int64
			if err := tx.Model(&dbAPRSReceived{}).Where("user_callsign = ? AND raw = ?", callsign, msg.Raw).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
		}
		var maxPos int
		_ = tx.Model(&dbAPRSReceived{}).Where("user_callsign = ?", callsign).Select("COALESCE(MAX(position), -1)").Scan(&maxPos).Error
		row := dbAPRSReceived{UserCallsign: callsign, Position: maxPos + 1, At: msg.At, From: msg.From, To: msg.To, Text: msg.Text, Raw: msg.Raw}
		if row.At == "" {
			row.At = now()
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return nil, err
	}
	if err := a.trimReceivedRows(callsign); err != nil {
		return nil, err
	}
	return a.loadReceivedHistory(callsign), nil
}

func (a *app) trimReceivedRows(callsign string) error {
	callsign = normalizeCallsign(callsign)
	rows := []dbAPRSReceived{}
	if err := a.db.Where("user_callsign = ?", callsign).Order("position DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}
	for i, row := range rows {
		cleaned := singleLineAPRSDetail(stripAPRSMessageID(row.Text))
		if cleaned != row.Text {
			if err := a.db.Model(&dbAPRSReceived{}).Where("id = ?", row.ID).Update("text", cleaned).Error; err != nil {
				return err
			}
		}
		if i >= receivedHistoryLimit {
			if err := a.db.Delete(&dbAPRSReceived{}, row.ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) sendAPRSMessage(source, destination, text, lang string) (sentAPRS, bool) {
	// Ordinary messages are split across as many APRS messages as needed.
	return a.sendAPRSMessageParts(source, destination, text, splitAPRSMessage(text), lang)
}

// aprsCommandTooLongDetail renders the over-length refusal.
//
// It formats a translated template, but falls back to English when the key is
// missing: a bare key reaching fmt.Sprintf with two arguments produces
// "aprs_command_too_long%!(EXTRA int=...)", which is worse than a plain sentence.
func (a *app) aprsCommandTooLongDetail(lang string, max, got int) string {
	const key = "aprs_command_too_long"
	tmpl := a.t(lang, key)
	if tmpl == key {
		tmpl = "Too long for a single APRS message: at most %d characters here (yours is %d). A command is never split."
	}
	return fmt.Sprintf(tmpl, max, got)
}

// sendAPRSCommand sends text as exactly one APRS message.
//
// Bot commands must not be split. The splitter prefixes every part with
// "[n/total] ", so a two-part "CQ HOTG ..." puts the command in part one as
// group "[1/2]" and the rest of the text in part two as group "[2/2]" — the
// command never reaches the group it names. A command that does not fit is
// refused instead, which the caller reports to the user.
func (a *app) sendAPRSCommand(source, destination, text, lang string) (sentAPRS, bool) {
	body := cleanAPRSBody(text)
	if len([]rune(body)) > aprsCommandMaxBody {
		return sentAPRS{
			At:     now(),
			Status: aprsStatusFailed,
			Text:   body,
			Parts: []sentAPRSPart{{
				Number: 1,
				Status: aprsStatusFailed,
				Detail: a.aprsCommandTooLongDetail(lang, aprsCommandMaxBody, len([]rune(body))),
			}},
		}, false
	}
	return a.sendAPRSMessageParts(source, destination, text, []string{body}, lang)
}

// sendAPRSMessageParts is the shared send path: validate, number the parts, send.
func (a *app) sendAPRSMessageParts(source, destination, text string, parts []string, lang string) (sentAPRS, bool) {
	if !validAPRSCallsign(source) {
		return sentAPRS{At: now(), Status: aprsStatusFailed, Parts: []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: a.t(lang, "aprs_invalid_source")}}}, false
	}
	if !validAPRSCallsign(destination) {
		return sentAPRS{At: now(), Status: aprsStatusFailed, Parts: []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: a.t(lang, "aprs_invalid_destination")}}}, false
	}
	from := aprsSSID0(source)
	to := normalizeAPRSCallsign(destination)
	passcode := aprsPasscode(from)
	sent := sentAPRS{At: now(), From: from, To: to, Text: cleanAPRSBody(text), Status: aprsStatusSent, Passcode: passcode}
	messageIDs := aprsMessageIDs(from, to, len(parts), time.Now())
	sentParts, allOK := a.sendAPRSParts(from, passcode, to, parts, messageIDs)
	sent.Parts = sentParts
	if len(sent.Parts) == 0 {
		sent.Parts = []sentAPRSPart{{Number: 1, Text: sent.Text, Status: aprsStatusFailed, Detail: "APRS-IS sender returned no part status"}}
		allOK = false
	}
	if !allOK {
		sent.Status = aprsStatusFailed
	}
	return sent, allOK
}

func (a *app) aprsStatusLabel(lang, status string) string {
	switch normalizeAPRSStatus(status) {
	case aprsStatusSent:
		return a.t(lang, "aprs_sent_status_sent")
	case aprsStatusFailed:
		return a.t(lang, "aprs_sent_status_failed")
	default:
		return status
	}
}

func normalizeAPRSStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "sent", "gesendet", "enviado", "envoye":
		return aprsStatusSent
	case "failed", "failure", "error", "fehler", "fallido", "echec":
		return aprsStatusFailed
	default:
		return status
	}
}

func (a *app) sendAPRSParts(source string, passcode int, destination string, parts []string, messageIDs []string) ([]sentAPRSPart, bool) {
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := netDialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		detail := fmt.Sprintf("APRS-IS unreachable at %s: %v", address, err)
		a.logAPRSSendResult(source, destination, "", "", detail, err)
		return []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: detail}}, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30*time.Second + time.Duration(len(parts))*3*time.Second))

	reader := bufio.NewReader(conn)
	loginLine := fmt.Sprintf("user %s pass %d vers HamNetBBS 0.1\r\n", source, passcode)
	if _, err := conn.Write([]byte(loginLine)); err != nil {
		a.logAPRSSendResult(source, destination, "", "", "", err)
		return []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: err.Error()}}, false
	}
	response, err := readAPRSISLoginResponse(reader)
	if err != nil {
		a.logAPRSSendResult(source, destination, "", "", response, err)
		return []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: err.Error()}}, false
	}

	out := make([]sentAPRSPart, 0, len(parts))
	allOK := true
	for i, part := range parts {
		messageID := ""
		if i < len(messageIDs) {
			messageID = messageIDs[i]
		}
		packetText := withAPRSMessageID(part, messageID)
		packet := formatAPRSMessagePacket(source, destination, packetText)
		status := aprsStatusSent
		detail := response
		err := writeAPRSISPacket(conn, packet)
		if err != nil {
			status = aprsStatusFailed
			detail = err.Error()
			allOK = false
		}
		a.logAPRSSendResult(source, destination, packetText, packet, response, err)
		out = append(out, sentAPRSPart{Number: i + 1, Text: part, Status: status, Detail: detail, MessageID: messageID})
		if !allOK {
			break
		}
		if i < len(parts)-1 {
			time.Sleep(750 * time.Millisecond)
		}
	}
	return out, allOK
}

// aprsAckSource is the station an acknowledgement is sent from: the addressee of
// the message being acknowledged, with its SSID intact.
//
// An APRS client matches an acknowledgement by comparing the ack's source with
// the addressee it used. Forcing the source to SSID 0 — as this did before — means
// a station that messaged EA7KLK-15 got "ack…" from EA7KLK-0 and, seeing a
// different station, left the message marked unacknowledged and retried it.
//
// A bare addressee stays bare. In AX.25 the SSID 0 station is written without the
// suffix, so "EA7KLK" and "EA7KLK-0" are the same station but not the same string,
// and it is the string the sender will compare.
func aprsAckSource(addressee string) string {
	return normalizeAPRSCallsign(addressee)
}

func (a *app) sendAPRSAck(source, destination, messageID string) error {
	source = aprsAckSource(source)
	destination = normalizeAPRSCallsign(destination)
	messageID = normalizeAPRSMessageID(messageID)
	if !validAPRSCallsign(source) {
		return fmt.Errorf("invalid APRS ACK source: %s", source)
	}
	// The passcode is derived from the base callsign, so an SSID does not change
	// it: EA7KLK-15 logs in with the EA7KLK validation code, which is what
	// APRS-IS expects.
	if !validAPRSCallsign(destination) {
		return fmt.Errorf("invalid APRS ACK destination: %s", destination)
	}
	if messageID == "" {
		return fmt.Errorf("missing APRS ACK message ID")
	}
	passcode := aprsPasscode(source)
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := netDialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		detail := fmt.Sprintf("APRS-IS unreachable at %s: %v", address, err)
		a.logAPRSSendResult(source, destination, "ack"+messageID, "", detail, err)
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(conn)
	loginLine := fmt.Sprintf("user %s pass %d vers HamNetBBS 0.1\r\n", source, passcode)
	if _, err := conn.Write([]byte(loginLine)); err != nil {
		a.logAPRSSendResult(source, destination, "ack"+messageID, "", "", err)
		return err
	}
	response, err := readAPRSISLoginResponse(reader)
	if err != nil {
		a.logAPRSSendResult(source, destination, "ack"+messageID, "", response, err)
		return err
	}

	packet := formatAPRSAckPacket(source, destination, messageID)
	err = writeAPRSISPacket(conn, packet)
	a.logAPRSSendResult(source, destination, "ack"+messageID, packet, response, err)
	return err
}

func (a *app) sendLoginAPRSBeacon(callsign string, profile userProfile) {
	if !profile.EnableAPRS || strings.TrimSpace(profile.Maidenhead) == "" {
		return
	}
	source := aprsSSID0(callsign)
	if !validAPRSCallsign(source) {
		a.logBBSAction(callsign, "aprs_beacon_skipped", "reason=%q source=%q", "invalid APRS callsign", source)
		return
	}
	lat, lon, err := maidenheadCenter(profile.Maidenhead)
	if err != nil {
		a.logBBSAction(callsign, "aprs_beacon_skipped", "locator=%q error=%q", profile.Maidenhead, err.Error())
		return
	}
	if err := a.sendAPRSBeacon(source, lat, lon, aprsBeaconText); err != nil {
		a.logBBSAction(callsign, "aprs_beacon_failed", "locator=%q lat=%.5f lon=%.5f error=%q", normalizeLocator(profile.Maidenhead), lat, lon, err.Error())
		return
	}
	a.logBBSAction(callsign, "aprs_beacon_sent", "locator=%q lat=%.5f lon=%.5f", normalizeLocator(profile.Maidenhead), lat, lon)
}

func (a *app) sendAPRSBeacon(source string, lat, lon float64, comment string) error {
	source = normalizeAPRSCallsign(source)
	if !validAPRSCallsign(source) {
		return fmt.Errorf("invalid APRS beacon source: %s", source)
	}
	passcode := aprsPasscode(source)
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := netDialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		detail := fmt.Sprintf("APRS-IS unreachable at %s: %v", address, err)
		a.logAPRSSendResult(source, "BEACON", comment, "", detail, err)
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(conn)
	loginLine := fmt.Sprintf("user %s pass %d vers HamNetBBS 0.1\r\n", source, passcode)
	if _, err := conn.Write([]byte(loginLine)); err != nil {
		a.logAPRSSendResult(source, "BEACON", comment, "", "", err)
		return err
	}
	response, err := readAPRSISLoginResponse(reader)
	if err != nil {
		a.logAPRSSendResult(source, "BEACON", comment, "", response, err)
		return err
	}

	packet := formatAPRSBeaconPacket(source, lat, lon, comment)
	err = writeAPRSISPacket(conn, packet)
	a.logAPRSSendResult(source, "BEACON", comment, packet, response, err)
	return err
}

func readAPRSISLoginResponse(reader *bufio.Reader) (string, error) {
	lines := []string{}
	for i := 0; i < 8; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			return strings.Join(lines, ""), err
		}
		lines = append(lines, line)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "logresp") {
			response := strings.Join(lines, "")
			if strings.Contains(lower, "unverified") || strings.Contains(lower, "invalid") {
				return response, fmt.Errorf("APRS-IS login failed: %s", strings.TrimSpace(line))
			}
			return response, nil
		}
	}
	response := strings.Join(lines, "")
	return response, fmt.Errorf("APRS-IS login response did not include logresp")
}

func writeAPRSISPacket(conn net.Conn, packet string) error {
	_, err := fmt.Fprintf(conn, "%s\r\n", packet)
	return err
}

func formatAPRSMessagePacket(source, destination, text string) string {
	return fmt.Sprintf("%s>APRS,TCPIP*::%-9s:%s", normalizeAPRSCallsign(source), normalizeAPRSCallsign(destination), text)
}

func formatAPRSAckPacket(source, destination, messageID string) string {
	return formatAPRSMessagePacket(source, destination, "ack"+normalizeAPRSMessageID(messageID))
}

func formatAPRSBeaconPacket(source string, lat, lon float64, comment string) string {
	return fmt.Sprintf("%s>APRS,TCPIP*:%s", normalizeAPRSCallsign(source), formatAPRSPosition(lat, lon, '\\', 'm', comment))
}

func formatAPRSPosition(lat, lon float64, symbolTable, symbolCode rune, comment string) string {
	latHemisphere := "N"
	if lat < 0 {
		latHemisphere = "S"
	}
	lonHemisphere := "E"
	if lon < 0 {
		lonHemisphere = "W"
	}
	lat = math.Abs(lat)
	lon = math.Abs(lon)
	latDegrees := int(lat)
	lonDegrees := int(lon)
	latMinutes := (lat - float64(latDegrees)) * 60
	lonMinutes := (lon - float64(lonDegrees)) * 60
	return fmt.Sprintf("!%02d%05.2f%s%c%03d%05.2f%s%c%s", latDegrees, latMinutes, latHemisphere, symbolTable, lonDegrees, lonMinutes, lonHemisphere, symbolCode, cleanAPRSBody(comment))
}

func maidenheadCenter(locator string) (float64, float64, error) {
	locator = strings.ToUpper(strings.TrimSpace(locator))
	if !maidenheadRE.MatchString(locator) {
		return 0, 0, fmt.Errorf("invalid Maidenhead locator: %s", locator)
	}
	lon := -180.0
	lat := -90.0
	lonSize := 20.0
	latSize := 10.0

	for i := 0; i < len(locator); i += 2 {
		switch i {
		case 0:
			lon += float64(locator[i]-'A') * lonSize
			lat += float64(locator[i+1]-'A') * latSize
		case 2, 6:
			lonSize /= 10
			latSize /= 10
			lon += float64(locator[i]-'0') * lonSize
			lat += float64(locator[i+1]-'0') * latSize
		case 4, 8:
			lonSize /= 24
			latSize /= 24
			lon += float64(locator[i]-'A') * lonSize
			lat += float64(locator[i+1]-'A') * latSize
		}
	}
	return lat + latSize/2, lon + lonSize/2, nil
}

func withAPRSMessageID(text, messageID string) string {
	messageID = normalizeAPRSMessageID(messageID)
	if messageID == "" {
		return text
	}
	return text + "{" + messageID
}

func aprsMessageIDs(source, destination string, count int, at time.Time) []string {
	if count < 1 {
		return nil
	}
	seed := at.UnixMilli()
	for _, r := range normalizeAPRSCallsign(source) + normalizeAPRSCallsign(destination) {
		seed += int64(r)
	}
	const space = int64(36 * 36 * 36 * 36 * 36)
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		value := (seed + int64(i)) % space
		id := strings.ToUpper(strconv.FormatInt(value, 36))
		if len(id) > 5 {
			id = id[len(id)-5:]
		}
		for len(id) < 5 {
			id = "0" + id
		}
		out = append(out, id)
	}
	return out
}

func normalizeAPRSMessageID(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if len(messageID) > 5 {
		messageID = messageID[:5]
	}
	var b strings.Builder
	for _, r := range messageID {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return strings.ToUpper(b.String())
}

func (a *app) logAPRSSendResult(source, destination, text, packet, response string, err error) {
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	body := strings.TrimRight(response, "\r\n")
	if body == "" {
		body = "<no APRS-IS response>"
	}
	logText := fmt.Sprintf(
		"%s APRS-IS send-message from=%s to=%s status=%s\nmessage=%s\npacket=%s\naprs-is-response-begin\n%s\naprs-is-response-end\n",
		now(),
		source,
		destination,
		status,
		text,
		packet,
		body,
	)
	appendLogFile(a.cfg.aprsLogFile, logText)
}

func aprsPasscode(callsign string) int {
	base := aprsBaseCallsign(callsign)
	code := 0x73e2
	for i, r := range base {
		if i%2 == 0 {
			code ^= int(r) << 8
		} else {
			code ^= int(r)
		}
	}
	return code & 0x7fff
}

func aprsBaseCallsign(callsign string) string {
	return strings.SplitN(normalizeAPRSCallsign(callsign), "-", 2)[0]
}

func aprsSSID0(callsign string) string {
	return aprsBaseCallsign(callsign) + "-0"
}

func cleanAPRSBody(text string) string {
	body := strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	body = strings.Join(strings.Fields(body), " ")
	return asciiSafe(strings.ToValidUTF8(body, "?"))
}

func splitAPRSMessage(text string) []string {
	body := cleanAPRSBody(text)
	if body == "" {
		return []string{""}
	}
	if len([]rune(body)) <= aprsMessageLimit {
		return []string{body}
	}
	total := len(splitRunes(body, aprsMessageLimit))
	for {
		prefixWidth := len([]rune(fmt.Sprintf("[%d/%d] ", total, total)))
		chunkLimit := aprsMessageLimit - prefixWidth
		if chunkLimit < 1 {
			chunkLimit = 1
		}
		chunks := splitRunes(body, chunkLimit)
		if len(chunks) == total {
			out := make([]string, 0, total)
			for i, chunk := range chunks {
				out = append(out, fmt.Sprintf("[%d/%d] %s", i+1, total, chunk))
			}
			return out
		}
		total = len(chunks)
	}
}

func splitRunes(text string, limit int) []string {
	if limit < 1 {
		limit = 1
	}
	runes := []rune(text)
	out := []string{}
	for len(runes) > 0 {
		n := limit
		if len(runes) < n {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}

func normalizeAPRSCallsign(callsign string) string {
	return strings.ToUpper(strings.TrimSpace(callsign))
}

func validAPRSCallsign(callsign string) bool {
	return aprsCallsignRE.MatchString(normalizeAPRSCallsign(callsign))
}

func validAPRSBaseCallsign(callsign string) bool {
	value := normalizeAPRSCallsign(callsign)
	return value != "" && !strings.Contains(value, "-") && aprsCallsignRE.MatchString(value+"-0")
}

func normalizeAPRSSSID(ssid string) string {
	return strings.TrimSpace(ssid)
}

func validAPRSSSID(ssid string) bool {
	if len(ssid) > 2 {
		return false
	}
	for _, r := range ssid {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitAPRSDestination(destination string) (string, string) {
	value := normalizeAPRSCallsign(destination)
	if value == "" {
		return "", "0"
	}
	parts := strings.SplitN(value, "-", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], normalizeAPRSSSID(parts[1])
}

func composeAPRSDestination(callsign, ssid string) string {
	callsign = normalizeAPRSCallsign(callsign)
	ssid = normalizeAPRSSSID(ssid)
	if ssid == "" {
		return callsign
	}
	return fmt.Sprintf("%s-%s", callsign, ssid)
}
