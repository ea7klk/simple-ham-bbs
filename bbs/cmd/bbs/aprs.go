package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	aprsStatusSent   = "sent"
	aprsStatusFailed = "failed"
)

func (a *app) aprsMenu(callsign string, profile userProfile, lang string) userProfile {
	users, _ := a.loadUsers()
	profile = users[callsign]
	for {
		header := fmt.Sprintf("%s: %s\n%s", a.t(lang, "aprs_status"), boolString(profile.EnableAPRS), a.t(lang, "aprs_ssid_info"))
		choice := a.runMenu(lang, a.t(lang, "menu_aprs"), header, []option{
			{"1", a.t(lang, "aprs_received_messages")},
			{"2", a.t(lang, "aprs_sent_messages")},
			{"3", a.t(lang, "aprs_send_message")},
			{"4", a.t(lang, "aprs_set_enabled")},
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
			_ = a.deleteReceivedRecord(history[idx].ID)
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
	for {
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_send_message"), [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}, {a.t(lang, "aprs_enable_required")}})
			return false
		}
		action, values, ok := a.runForm(lang, a.t(lang, "aprs_send_message"), []formField{
			{name: "destination", label: a.t(lang, "aprs_destination_callsign"), value: destination, required: true, limit: 9, normalizer: normalizeAPRSCallsign, validator: validAPRSCallsign, invalidText: a.t(lang, "aprs_invalid_destination")},
			{name: "text", label: a.t(lang, "aprs_text"), kind: fieldTextArea, required: true, limit: 2000},
		}, []string{"send", "cancel"})
		if !ok || action == "cancel" {
			return false
		}
		a.showSendingAPRS(lang, values["destination"])
		sent, okSend := a.sendAPRSMessage(callsign, values["destination"], values["text"], lang)
		_ = a.addSent(callsign, sent)
		if !okSend {
			detail := ""
			if len(sent.Parts) > 0 {
				detail = sent.Parts[len(sent.Parts)-1].Detail
			}
			if detail == "" {
				detail = sent.Status
			}
			a.showInfoActions(lang, a.t(lang, "aprs_send_failed"), [][]string{{detail}}, []option{{"o", a.t(lang, "ok_button")}})
			continue
		}
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
			_ = a.deleteSentRecord(history[idx].ID)
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
	return fmt.Sprintf("%-9s  %-17s  %s", item.To, item.At, truncateText(singleLineAPRSDetail(item.Text), 34))
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
		rows = append(rows, []string{a.t(lang, "aprs_raw_packet"), singleLineAPRSDetail(item.Raw)})
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
		item := sentAPRS{ID: row.ID, At: row.At, From: row.From, To: row.To, Text: singleLineAPRSDetail(row.Text), Status: normalizeAPRSStatus(row.Status), Passcode: row.Passcode}
		for _, part := range row.Parts {
			item.Parts = append(item.Parts, sentAPRSPart{Number: part.Number, Text: singleLineAPRSDetail(part.Text), Status: normalizeAPRSStatus(part.Status), Detail: singleLineAPRSDetail(part.Detail)})
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
		row := dbAPRSSent{UserCallsign: callsign, Position: maxPos + 1, At: sent.At, From: sent.From, To: sent.To, Text: sent.Text, Status: normalizeAPRSStatus(sent.Status), Passcode: sent.Passcode}
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
			if err := tx.Create(&dbAPRSSentPart{SentID: row.ID, Number: number, Text: singleLineAPRSDetail(part.Text), Status: normalizeAPRSStatus(part.Status), Detail: singleLineAPRSDetail(part.Detail)}).Error; err != nil {
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
	if err := a.checkAPRSReachable(); err != nil {
		sent.Status = aprsStatusFailed
		sent.Parts = []sentAPRSPart{{Number: 1, Text: sent.Text, Status: sent.Status, Detail: err.Error()}}
		a.logAPRSDResult(from, to, sent.Text, nil, err)
		return sent, false
	}
	configPath, cleanup, err := a.writeAPRSDConfig(from, passcode)
	if err != nil {
		sent.Status = aprsStatusFailed
		sent.Parts = []sentAPRSPart{{Number: 1, Text: sent.Text, Status: sent.Status, Detail: err.Error()}}
		return sent, false
	}
	defer cleanup()
	allOK := true
	for i, part := range parts {
		status := aprsStatusSent
		detail := ""
		if err := a.runAPRSDMessage(configPath, from, passcode, to, part); err != nil {
			status = aprsStatusFailed
			detail = err.Error()
			allOK = false
		}
		sent.Parts = append(sent.Parts, sentAPRSPart{Number: i + 1, Text: part, Status: status, Detail: detail})
		if !allOK {
			break
		}
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

func (a *app) writeAPRSDConfig(source string, passcode int) (string, func(), error) {
	dir, err := os.MkdirTemp("", "hamnet-bbs-aprsd-")
	if err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(dir, "aprsd.conf")
	config := fmt.Sprintf(`[DEFAULT]
callsign = %s
owner_callsign = %s
enable_save = false
enable_beacon = false
enabled_plugins =

[aprs_network]
enabled = true
password = %d
host = %s
port = %d
`, source, aprsBaseCallsign(source), passcode, a.cfg.aprsServer, a.cfg.aprsPort)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

func (a *app) runAPRSDMessage(configPath, source string, passcode int, destination, text string) error {
	if !exists(a.cfg.aprsdBin) {
		err := fmt.Errorf("%s: %s", a.t("en", "aprsd_missing"), a.cfg.aprsdBin)
		a.logAPRSDResult(source, destination, text, nil, err)
		return err
	}
	cmd := exec.Command(
		a.cfg.aprsdBin,
		"send-message",
		"--config", configPath,
		"--aprs-login", source,
		"--aprs-password", fmt.Sprintf("%d", passcode),
		"--no-ack",
		"--loglevel", "debug",
		destination,
		text,
	)
	output, err := cmd.CombinedOutput()
	a.logAPRSDResult(source, destination, text, output, err)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *app) logAPRSDResult(source, destination, text string, output []byte, err error) {
	status := "ok"
	if err != nil {
		status = "error: " + err.Error()
	}
	body := strings.TrimRight(string(output), "\r\n")
	if body == "" {
		body = "<no aprsd output>"
	}
	logText := fmt.Sprintf(
		"%s APRSD send-message from=%s to=%s status=%s\nmessage=%s\naprsd-output-begin\n%s\naprsd-output-end\n",
		now(),
		source,
		destination,
		status,
		text,
		body,
	)
	if err := os.MkdirAll(filepath.Dir(a.cfg.aprsLogFile), 0o755); err == nil {
		if file, err := os.OpenFile(a.cfg.aprsLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = file.WriteString(logText)
			_ = file.Close()
			return
		}
	}
	if err := os.WriteFile("/proc/1/fd/1", []byte(logText), 0o600); err == nil {
		return
	}
	_, _ = fmt.Fprint(os.Stderr, logText)
}

func (a *app) checkAPRSReachable() error {
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return fmt.Errorf("APRS-IS unreachable at %s: %w", address, err)
	}
	_ = conn.Close()
	return nil
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
