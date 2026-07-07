package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
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
			a.showInfo(lang, a.t(lang, "aprs_received_messages"), [][]string{{a.t(lang, "aprs_received_placeholder")}, {a.t(lang, "aprs_ssid_info")}})
		case "3":
			a.sendAPRS(callsign, profile, lang)
		case "q":
			return profile
		}
	}
}

func (a *app) sendAPRS(callsign string, profile userProfile, lang string) {
	history := a.trimSent(callsign)
	for {
		rows := [][]string{{a.t(lang, "aprs_status"), boolString(profile.EnableAPRS)}, {a.t(lang, "aprs_ssid_info")}}
		for _, item := range reverseSent(history) {
			rows = append(rows, []string{item.At, item.To + " / " + item.Status + " / " + item.Text})
		}
		if len(history) == 0 {
			rows = append(rows, []string{a.t(lang, "aprs_no_sent_messages")})
		}
		if !profile.EnableAPRS {
			a.showInfo(lang, a.t(lang, "aprs_send_message"), append(rows, []string{a.t(lang, "aprs_enable_required")}))
			return
		}
		action, values, ok := a.runForm(lang, a.t(lang, "aprs_send_message"), []formField{
			{name: "destination", label: a.t(lang, "aprs_destination_callsign"), required: true, limit: 9, normalizer: normalizeAPRSCallsign, validator: validAPRSCallsign, invalidText: a.t(lang, "aprs_invalid_destination")},
			{name: "text", label: a.t(lang, "aprs_text"), kind: fieldTextArea, required: true, limit: aprsMessageLimit},
		}, []string{"send", "cancel"})
		if !ok || action == "cancel" {
			return
		}
		status := a.t(lang, "aprs_sent_status_sent")
		okSend, detail := a.sendAPRSMessage(callsign, values["destination"], values["text"])
		if !okSend {
			status = a.t(lang, "aprs_sent_status_failed")
			a.showInfo(lang, a.t(lang, "aprs_send_failed"), [][]string{{detail}})
		}
		history = a.addSent(callsign, values["destination"], values["text"], status)
	}
}

func (a *app) trimSent(callsign string) []sentAPRS {
	all := map[string][]sentAPRS{}
	_ = readJSON(a.cfg.aprsSentFile, &all, map[string][]sentAPRS{})
	key := normalizeCallsign(callsign)
	history := all[key]
	if len(history) > sentHistoryLimit {
		history = history[len(history)-sentHistoryLimit:]
	}
	all[key] = history
	_ = writeJSON(a.cfg.aprsSentFile, all)
	return history
}

func (a *app) addSent(callsign, destination, text, status string) []sentAPRS {
	all := map[string][]sentAPRS{}
	_ = readJSON(a.cfg.aprsSentFile, &all, map[string][]sentAPRS{})
	key := normalizeCallsign(callsign)
	history := append(all[key], sentAPRS{At: now(), To: normalizeAPRSCallsign(destination), Text: text, Status: status})
	if len(history) > sentHistoryLimit {
		history = history[len(history)-sentHistoryLimit:]
	}
	all[key] = history
	_ = writeJSON(a.cfg.aprsSentFile, all)
	return history
}

func (a *app) sendAPRSMessage(source, destination, text string) (bool, string) {
	if !validAPRSCallsign(source) {
		return false, a.t("en", "aprs_invalid_source")
	}
	if !validAPRSCallsign(destination) {
		return false, a.t("en", "aprs_invalid_destination")
	}
	packet := aprsPacket(source, destination, text)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort)), 10*time.Second)
	if err != nil {
		return false, err.Error()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = fmt.Fprintf(conn, "%s\r\n", a.aprsLogin(source))
	_, _ = bufio.NewReader(conn).ReadString('\n')
	_, err = fmt.Fprintf(conn, "%s\r\n", packet)
	if err != nil {
		return false, err.Error()
	}
	return true, packet
}

func (a *app) aprsLogin(callsign string) string {
	source := aprsBaseCallsign(callsign)
	return fmt.Sprintf("user %s pass %d vers %s %s", source, aprsPasscode(source), a.cfg.aprsApp, a.cfg.aprsVersion)
}

func aprsPacket(source, destination, text string) string {
	body := strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	if len(body) > aprsMessageLimit {
		body = body[:aprsMessageLimit]
	}
	body = strings.ToValidUTF8(body, "?")
	return fmt.Sprintf("%s>APRS,TCPIP*::%-9s:%s", aprsBaseCallsign(source), normalizeAPRSCallsign(destination), asciiSafe(body))
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

func normalizeAPRSCallsign(callsign string) string {
	return strings.ToUpper(strings.TrimSpace(callsign))
}

func validAPRSCallsign(callsign string) bool {
	return aprsCallsignRE.MatchString(normalizeAPRSCallsign(callsign))
}
