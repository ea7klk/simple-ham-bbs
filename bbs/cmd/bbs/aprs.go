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
			{"5", a.t(lang, "aprs_join_aprsph")},
			{"6", a.t(lang, "aprs_set_enabled")},
			{"q", a.t(lang, "menu_quit")},
		})
		switch choice {
		case "1":
			a.showReceivedAPRS(callsign, profile, lang)
		case "2":
			a.showSentAPRS(callsign, profile, lang)
		case "3":
			a.sendAPRS(callsign, profile, lang)
		case "4":
			a.joinAPRSThursday(callsign, profile, lang)
		case "5":
			a.joinAPRSPH(callsign, profile, lang)
		case "6":
			_, values, ok := a.runForm(lang, a.t(lang, "aprs_enable_title"), []formField{{name: "enable_aprs", label: a.t(lang, "enable_aprs"), kind: fieldChoice, value: boolString(profile.EnableAPRS), choices: []option{{"false", "false"}, {"true", "true"}}}}, []string{"save", "cancel"})
			if ok {
				profile.EnableAPRS = values["enable_aprs"] == "true"
				profile.LastSeen = now()
				users[callsign] = profile
				_ = a.saveUsers(users)
			}
		case "q":
			return profile
		}
	}
}

func (a *app) showReceivedAPRS(callsign string, profile userProfile, lang string) {
	for {
		history := reverseReceived(a.trimReceived(callsign))
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_received_messages"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return
		}
		if len(history) == 0 {
			a.showInfoActions(lang, a.t(lang, "aprs_received_messages"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_no_received_messages")}}, []option{{"q", a.t(lang, "back_button")}})
			return
		}
		opts := []option{}
		for i, item := range history {
			opts = append(opts, option{strconv.Itoa(i + 1), a.receivedAPRSListLabel(item)})
		}
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		choice := a.runMenu(lang, a.t(lang, "aprs_received_messages"), a.t(lang, "aprs_latest_received"), opts)
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
				_ = a.deleteReceivedRecord(history[idx].ID)
				a.logBBSAction(callsign, "aprs_received_delete", "from=%q at=%q", history[idx].From, history[idx].At)
			}
		case "r":
			a.sendAPRSForm(callsign, profile, lang, normalizeAPRSCallsign(history[idx].From))
		}
	}
}

func (a *app) sendAPRS(callsign string, profile userProfile, lang string) {
	for {
		if !a.sendAPRSForm(callsign, profile, lang, "") {
			return
		}
	}
}

func (a *app) sendAPRSForm(callsign string, profile userProfile, lang, destination string) bool {
	destCall, destSSID := splitAPRSDestination(destination)
	for {
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_send_message"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return false
		}
		action, values, ok := a.runForm(lang, a.t(lang, "aprs_send_message"), []formField{
			{name: "destination", label: a.t(lang, "aprs_destination_callsign"), value: destCall, required: true, limit: 10, width: 10, sameLine: true, normalizer: normalizeAPRSCallsign, validator: validAPRSBaseCallsign, invalidText: a.t(lang, "aprs_invalid_destination")},
			{name: "destination_ssid", label: a.t(lang, "aprs_destination_ssid"), value: destSSID, limit: 2, width: 2, sameLine: true, normalizer: normalizeAPRSSSID, validator: validAPRSSSID, invalidText: a.t(lang, "aprs_invalid_ssid")},
			{name: "text", label: a.t(lang, "aprs_text"), kind: fieldTextArea, required: true, limit: 2000},
		}, []string{"send", "cancel"})
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

func (a *app) joinAPRSThursday(callsign string, profile userProfile, lang string) {
	if time.Now().UTC().Weekday() != time.Thursday {
		a.showInfoActions(lang, a.t(lang, "aprs_join_aprs_thursday"), [][]string{{a.t(lang, "aprs_not_thursday_warning")}}, []option{{"q", a.t(lang, "back_button")}})
		return
	}
	a.sendANSRVRMessage(callsign, profile, lang, a.t(lang, "aprs_join_aprs_thursday"), "HOTG")
}

func (a *app) joinAPRSPH(callsign string, profile userProfile, lang string) {
	a.sendANSRVRMessage(callsign, profile, lang, a.t(lang, "aprs_join_aprsph"), "CQ")
}

func (a *app) sendANSRVRMessage(callsign string, profile userProfile, lang, title, prefix string) bool {
	for {
		if !profile.EnableAPRS {
			a.showInfo(lang, title, [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return false
		}
		action, values, ok := a.runForm(lang, title, []formField{
			{name: "text", label: a.t(lang, "aprs_ansrvr_text"), kind: fieldTextArea, required: true, limit: 1900},
		}, []string{"send", "cancel"})
		if !ok || action == "cancel" {
			return false
		}
		text := prefix + " " + strings.TrimSpace(values["text"])
		a.showSendingAPRS(lang, "ANSRVR")
		sent, okSend := a.sendAPRSMessage(callsign, "ANSRVR", text, lang)
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

func (a *app) showSentAPRS(callsign string, profile userProfile, lang string) {
	for {
		history := reverseSent(a.trimSent(callsign))
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
		choice := a.runMenu(lang, a.t(lang, "aprs_sent_messages"), a.t(lang, "aprs_latest_sent"), opts)
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
				_ = a.deleteSentRecord(history[idx].ID)
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
	return fmt.Sprintf("%-9s  %-17s  %s", item.From, item.At, truncateText(singleLineAPRSDetail(stripAPRSMessageID(item.Text)), 34))
}

func (a *app) sentAPRSSummary(lang string, item sentAPRS) string {
	status := a.aprsStatusLabel(lang, item.Status)
	return fmt.Sprintf("%s -> %s / %s / %d %s / %s", item.From, item.To, status, len(item.Parts), a.t(lang, "aprs_parts"), singleLineAPRSDetail(item.Text))
}

func (a *app) sentAPRSListLabel(item sentAPRS) string {
	return fmt.Sprintf("%-1s  %-17s  %-9s  %s", sentAckIcon(item), item.At, item.To, truncateText(singleLineAPRSDetail(item.Text), 31))
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

func singleLineAPRSDetail(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
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
	if !validAPRSCallsign(source) {
		return sentAPRS{At: now(), Status: aprsStatusFailed, Parts: []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: a.t(lang, "aprs_invalid_source")}}}, false
	}
	if !validAPRSCallsign(destination) {
		return sentAPRS{At: now(), Status: aprsStatusFailed, Parts: []sentAPRSPart{{Number: 1, Status: aprsStatusFailed, Detail: a.t(lang, "aprs_invalid_destination")}}}, false
	}
	from := aprsSSID0(source)
	to := normalizeAPRSCallsign(destination)
	passcode := aprsPasscode(from)
	parts := splitAPRSMessage(text)
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

func (a *app) sendAPRSAck(source, destination, messageID string) error {
	source = normalizeAPRSCallsign(source)
	destination = normalizeAPRSCallsign(destination)
	messageID = normalizeAPRSMessageID(messageID)
	if !validAPRSCallsign(source) {
		return fmt.Errorf("invalid APRS ACK source: %s", source)
	}
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
