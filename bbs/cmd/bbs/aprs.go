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
			{"1", a.t(lang, "aprs_set_enabled")},
			{"2", a.t(lang, "aprs_received_messages")},
			{"3", a.t(lang, "aprs_send_message")},
			{"q", a.t(lang, "menu_quit")},
		})
		switch choice {
		case "1":
			_, values, ok := a.runForm(lang, a.t(lang, "aprs_enable_title"), []formField{{name: "enable_aprs", label: a.t(lang, "enable_aprs"), kind: fieldChoice, value: boolString(profile.EnableAPRS), choices: []option{{"false", "false"}, {"true", "true"}}}}, []string{"save", "cancel"})
			if ok {
				profile.EnableAPRS = values["enable_aprs"] == "true"
				profile.LastSeen = now()
				users[callsign] = profile
				_ = a.saveUsers(users)
			}
		case "2":
			a.showReceivedAPRS(callsign, profile, lang)
		case "3":
			a.sendAPRS(callsign, profile, lang)
		case "q":
			return profile
		}
	}
}

func (a *app) showReceivedAPRS(callsign string, profile userProfile, lang string) {
	history := a.trimReceived(callsign)
	rows := a.aprsReceivedRows(lang, profile.EnableAPRS, history)
	if !profile.EnableAPRS {
		rows = append(rows, []string{a.t(lang, "aprs_enable_required")})
	}
	a.showInfoActions(lang, a.t(lang, "aprs_received_messages"), rows, []option{{"q", a.t(lang, "back_button")}})
}

func (a *app) sendAPRS(callsign string, profile userProfile, lang string) {
	history := a.trimSent(callsign)
	for {
		rows := a.aprsSentRows(lang, profile.EnableAPRS, history)
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_send_message"), append(rows, []string{a.t(lang, "aprs_enable_required")}))
			return
		}
		action := a.showInfoActions(lang, a.t(lang, "aprs_send_message"), rows, []option{{"s", a.t(lang, "send_button")}, {"q", a.t(lang, "back_button")}})
		if action != "s" {
			return
		}
		action, values, ok := a.runForm(lang, a.t(lang, "aprs_send_message"), []formField{
			{name: "destination", label: a.t(lang, "aprs_destination_callsign"), required: true, limit: 9, normalizer: normalizeAPRSCallsign, validator: validAPRSCallsign, invalidText: a.t(lang, "aprs_invalid_destination")},
			{name: "text", label: a.t(lang, "aprs_text"), kind: fieldTextArea, required: true, limit: 2000},
		}, []string{"send", "cancel"})
		if !ok || action == "cancel" {
			continue
		}
		a.showSendingAPRS(lang, values["destination"])
		sent, okSend := a.sendAPRSMessage(callsign, values["destination"], values["text"], lang)
		history = a.addSent(callsign, sent)
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
	return fmt.Sprintf("%s -> %s / %s", item.From, item.To, stripAPRSMessageID(item.Text))
}

func (a *app) sentAPRSSummary(lang string, item sentAPRS) string {
	status := a.aprsStatusLabel(lang, item.Status)
	return fmt.Sprintf("%s -> %s / %s / %d %s / %s", item.From, item.To, status, len(item.Parts), a.t(lang, "aprs_parts"), item.Text)
}

func (a *app) aprsSentResultRows(lang string, item sentAPRS) [][]string {
	rows := [][]string{
		{a.t(lang, "from"), item.From},
		{a.t(lang, "aprs_destination_callsign"), item.To},
		{a.t(lang, "aprs_parts"), fmt.Sprintf("%d", len(item.Parts))},
	}
	for _, part := range item.Parts {
		rows = append(rows, []string{fmt.Sprintf("%d", part.Number), a.aprsStatusLabel(lang, part.Status) + " / " + part.Text})
	}
	return rows
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
	all := map[string][]sentAPRS{}
	_ = readJSON(a.cfg.aprsSentFile, &all, map[string][]sentAPRS{})
	key := normalizeCallsign(callsign)
	history := all[key]
	for i := range history {
		history[i].Status = normalizeAPRSStatus(history[i].Status)
		for j := range history[i].Parts {
			history[i].Parts[j].Status = normalizeAPRSStatus(history[i].Parts[j].Status)
		}
	}
	if len(history) > sentHistoryLimit {
		history = history[len(history)-sentHistoryLimit:]
	}
	all[key] = history
	_ = writeJSON(a.cfg.aprsSentFile, all)
	return history
}

func (a *app) trimReceived(callsign string) []receivedAPRS {
	all := map[string][]receivedAPRS{}
	_ = readJSON(a.cfg.aprsReceivedFile, &all, map[string][]receivedAPRS{})
	key := normalizeCallsign(callsign)
	history := all[key]
	for i := range history {
		history[i].Text = stripAPRSMessageID(history[i].Text)
	}
	if len(history) > receivedHistoryLimit {
		history = history[len(history)-receivedHistoryLimit:]
	}
	all[key] = history
	_ = writeJSON(a.cfg.aprsReceivedFile, all)
	return history
}

func (a *app) addSent(callsign string, sent sentAPRS) []sentAPRS {
	all := map[string][]sentAPRS{}
	_ = readJSON(a.cfg.aprsSentFile, &all, map[string][]sentAPRS{})
	key := normalizeCallsign(callsign)
	history := append(all[key], sent)
	if len(history) > sentHistoryLimit {
		history = history[len(history)-sentHistoryLimit:]
	}
	all[key] = history
	_ = writeJSON(a.cfg.aprsSentFile, all)
	return history
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
