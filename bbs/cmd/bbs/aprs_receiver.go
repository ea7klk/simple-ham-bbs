package main

import (
	"bufio"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var aprsMultipartRE = regexp.MustCompile(`^\[([0-9]+)/([0-9]+)\]\s*(.*)$`)

type parsedAPRSPacket struct {
	Message receivedAPRS
	AckID   string
	RejID   string
}

type aprsMultipartBuffer struct {
	From     string
	To       string
	Total    int
	Parts    map[int]string
	RawParts map[int]string
	FirstAt  string
	Updated  time.Time
}

func (a *app) runAPRSReceiver() error {
	login := a.aprsReceiverLogin()
	if !validAPRSCallsign(login) {
		return fmt.Errorf("invalid APRS receiver callsign: %s", a.cfg.aprsReceiverCallsign)
	}
	passcode := aprsPasscode(login)
	restartAt := time.Now().Add(aprsReceiverRestartInterval)
	a.logAPRSReceiver("APRS receiver starting login=%s server=%s:%d filter=t/m restart_at=%s", login, a.cfg.aprsServer, a.cfg.aprsPort, restartAt.UTC().Format("2006-01-02 15:04 UTC"))
	for {
		if !time.Now().Before(restartAt) {
			a.logAPRSReceiver("APRS receiver hourly restart requested")
			return nil
		}
		if err := a.receiveAPRSLoop(login, passcode, restartAt); err != nil {
			a.logAPRSReceiver("APRS receiver disconnected: %v", err)
		}
		if !time.Now().Before(restartAt) {
			a.logAPRSReceiver("APRS receiver hourly restart requested")
			return nil
		}
		time.Sleep(minDuration(10*time.Second, time.Until(restartAt)))
	}
}

func (a *app) aprsReceiverLogin() string {
	configured := aprsBaseCallsign(a.cfg.aprsReceiverCallsign)
	if configured != "" && validAPRSCallsign(configured) {
		return configured
	}
	for _, callsign := range sortedKeys(a.cfg.sysops) {
		base := aprsBaseCallsign(callsign)
		if validAPRSCallsign(base) {
			return base
		}
	}
	return "N0CALL"
}

func (a *app) receiveAPRSLoop(login string, passcode int, restartAt time.Time) error {
	address := net.JoinHostPort(a.cfg.aprsServer, strconv.Itoa(a.cfg.aprsPort))
	conn, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(restartAt)
	a.logAPRSReceiver("APRS receiver connected to %s", address)
	if _, err := fmt.Fprintf(conn, "user %s pass %d vers HamNetBBS 0.1 filter t/m\r\n", login, passcode); err != nil {
		return err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	multipart := map[string]*aprsMultipartBuffer{}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			a.logAPRSReceiver("APRS-IS: %s", line)
			continue
		}
		packet, ok := parseAPRSMessagePacket(line)
		if !ok {
			continue
		}
		if packet.AckID != "" {
			matched, err := a.markSentAPRSAck(packet.Message.From, packet.Message.To, packet.AckID)
			if err != nil {
				a.logAPRSReceiver("APRS ack update error from=%s to=%s id=%s: %v", packet.Message.From, packet.Message.To, packet.AckID, err)
				continue
			}
			a.logAPRSReceiver("APRS ack received from=%s to=%s id=%s matched=%t", packet.Message.From, packet.Message.To, packet.AckID, matched)
			a.logBBSAction(aprsRecipientKey(packet.Message.To), "aprs_ack_received", "from=%q to=%q id=%q matched=%t", packet.Message.From, packet.Message.To, packet.AckID, matched)
			continue
		}
		if packet.RejID != "" {
			a.logAPRSReceiver("APRS rejection received from=%s to=%s id=%s", packet.Message.From, packet.Message.To, packet.RejID)
			continue
		}
		msg := packet.Message
		if msg.MessageID != "" {
			user, enabled, err := a.aprsRecipientEnabled(msg.To)
			if err != nil {
				a.logAPRSReceiver("APRS ack eligibility error to=%s id=%s: %v", msg.To, msg.MessageID, err)
			} else if enabled {
				if err := sendAPRSAck(conn, msg.To, msg.From, msg.MessageID); err != nil {
					a.logAPRSReceiver("APRS ack send error user=%s from=%s to=%s id=%s: %v", user, msg.To, msg.From, msg.MessageID, err)
				} else {
					a.logAPRSReceiver("APRS ack sent user=%s from=%s to=%s id=%s", user, msg.To, msg.From, msg.MessageID)
					a.logBBSAction(user, "aprs_ack_sent", "from=%q to=%q id=%q", msg.To, msg.From, msg.MessageID)
				}
			}
		}
		ready, waiting := collectAPRSMessagePart(multipart, msg)
		if waiting {
			a.logAPRSReceiver("APRS multipart message part buffered from=%s to=%s text=%s", msg.From, msg.To, msg.Text)
			pruneAPRSMessageParts(multipart, 30*time.Minute)
			continue
		}
		for _, item := range ready {
			user, saved, err := a.storeReceivedAPRS(item)
			if err != nil {
				a.logAPRSReceiver("APRS receiver store error from=%s to=%s: %v", item.From, item.To, err)
				continue
			}
			if saved {
				a.logAPRSReceiver("APRS received message user=%s from=%s to=%s text=%s", user, item.From, item.To, item.Text)
			}
		}
		pruneAPRSMessageParts(multipart, 30*time.Minute)
	}
	return scanner.Err()
}

func collectAPRSMessagePart(buffers map[string]*aprsMultipartBuffer, msg receivedAPRS) ([]receivedAPRS, bool) {
	part, total, body, ok := parseAPRSMultipartPrefix(msg.Text)
	if !ok {
		return []receivedAPRS{msg}, false
	}
	if total <= 1 {
		msg.Text = body
		return []receivedAPRS{msg}, false
	}
	key := fmt.Sprintf("%s|%s|%d", normalizeAPRSCallsign(msg.From), normalizeAPRSCallsign(msg.To), total)
	buf := buffers[key]
	if buf == nil {
		buf = &aprsMultipartBuffer{From: msg.From, To: msg.To, Total: total, Parts: map[int]string{}, RawParts: map[int]string{}, FirstAt: msg.At}
		buffers[key] = buf
	}
	buf.Parts[part] = body
	buf.RawParts[part] = msg.Raw
	buf.Updated = time.Now()
	if len(buf.Parts) < total {
		return nil, true
	}
	parts := make([]string, 0, total)
	rawParts := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		text, ok := buf.Parts[i]
		if !ok {
			return nil, true
		}
		parts = append(parts, text)
		rawParts = append(rawParts, buf.RawParts[i])
	}
	delete(buffers, key)
	at := buf.FirstAt
	if at == "" {
		at = msg.At
	}
	return []receivedAPRS{{
		At:   at,
		From: buf.From,
		To:   buf.To,
		Text: strings.Join(parts, ""),
		Raw:  strings.Join(rawParts, "\n"),
	}}, false
}

func parseAPRSMultipartPrefix(text string) (int, int, string, bool) {
	matches := aprsMultipartRE.FindStringSubmatch(strings.TrimLeft(text, " \t"))
	if len(matches) != 4 {
		return 0, 0, text, false
	}
	part, errPart := strconv.Atoi(matches[1])
	total, errTotal := strconv.Atoi(matches[2])
	if errPart != nil || errTotal != nil || part < 1 || total < 1 || part > total {
		return 0, 0, text, false
	}
	return part, total, matches[3], true
}

func pruneAPRSMessageParts(buffers map[string]*aprsMultipartBuffer, maxAge time.Duration) {
	now := time.Now()
	for key, buf := range buffers {
		if !buf.Updated.IsZero() && now.Sub(buf.Updated) > maxAge {
			delete(buffers, key)
		}
	}
}

func parseAPRSMessageLine(raw string) (receivedAPRS, bool) {
	packet, ok := parseAPRSMessagePacket(raw)
	if !ok || packet.AckID != "" || packet.RejID != "" {
		return receivedAPRS{}, false
	}
	return packet.Message, true
}

func parseAPRSMessagePacket(raw string) (parsedAPRSPacket, bool) {
	header, info, ok := strings.Cut(raw, ":")
	if !ok || !strings.HasPrefix(info, ":") || len(info) < 11 || info[10] != ':' {
		return parsedAPRSPacket{}, false
	}
	source, _, ok := strings.Cut(header, ">")
	if !ok {
		return parsedAPRSPacket{}, false
	}
	to := strings.TrimSpace(info[1:10])
	text := strings.TrimSpace(info[11:])
	if to == "" || text == "" {
		return parsedAPRSPacket{}, false
	}
	msg := receivedAPRS{
		At:   now(),
		From: normalizeAPRSCallsign(source),
		To:   normalizeAPRSCallsign(to),
		Text: stripAPRSMessageID(text),
		Raw:  raw,
	}
	if ackID, ok := aprsResponseID(text, "ack"); ok {
		return parsedAPRSPacket{Message: msg, AckID: ackID}, true
	}
	if rejID, ok := aprsResponseID(text, "rej"); ok {
		return parsedAPRSPacket{Message: msg, RejID: rejID}, true
	}
	msg.Text, msg.MessageID = splitAPRSMessageID(text)
	if msg.Text == "" {
		return parsedAPRSPacket{}, false
	}
	return parsedAPRSPacket{Message: msg}, true
}

func (a *app) storeReceivedAPRS(msg receivedAPRS) (string, bool, error) {
	msg.Text = stripAPRSMessageID(msg.Text)
	if isAPRSAckMessage(msg.Text) {
		return aprsRecipientKey(msg.To), false, nil
	}
	users, err := a.loadUsers()
	if err != nil {
		return "", false, err
	}
	key := aprsRecipientKey(msg.To)
	profile, ok := users[key]
	if !ok || profile.Disabled || !profile.EnableAPRS {
		return key, false, nil
	}
	if msg.Raw != "" {
		var count int64
		if err := a.db.Model(&dbAPRSReceived{}).Where("user_callsign = ? AND raw = ?", key, msg.Raw).Count(&count).Error; err != nil {
			return key, false, err
		}
		if count > 0 {
			return key, false, nil
		}
	}
	_, err = a.addReceivedRecord(key, msg)
	return key, err == nil, err
}

func (a *app) aprsRecipientEnabled(to string) (string, bool, error) {
	users, err := a.loadUsers()
	if err != nil {
		return "", false, err
	}
	key := aprsRecipientKey(to)
	profile, ok := users[key]
	return key, ok && !profile.Disabled && profile.EnableAPRS, nil
}

func sendAPRSAck(conn net.Conn, source, destination, messageID string) error {
	return writeAPRSISPacket(conn, formatAPRSAckPacket(source, destination, messageID))
}

func isAPRSAckMessage(text string) bool {
	text = strings.ToLower(strings.TrimSpace(stripAPRSMessageID(text)))
	return isAPRSResponseToken(text, "ack") || isAPRSResponseToken(text, "rej")
}

func aprsResponseID(text, prefix string) (string, bool) {
	text = strings.TrimSpace(text)
	if body, _, ok := strings.Cut(text, "{"); ok {
		text = body
	}
	suffix, ok := strings.CutPrefix(strings.ToLower(text), prefix)
	if !ok || suffix == "" || len([]rune(suffix)) > 5 {
		return "", false
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return "", false
		}
	}
	return normalizeAPRSMessageID(suffix), true
}

func isAPRSResponseToken(text, prefix string) bool {
	suffix, ok := strings.CutPrefix(text, prefix)
	if !ok {
		return false
	}
	if suffix == "" {
		return true
	}
	if len([]rune(suffix)) > 5 {
		return false
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func (a *app) normalizeReceivedAPRSStore() error {
	users, err := a.loadUsers()
	if err != nil {
		return err
	}
	for callsign := range users {
		if err := a.trimReceivedRows(callsign); err != nil {
			return err
		}
	}
	return nil
}

func aprsRecipientKey(to string) string {
	return normalizeCallsign(aprsBaseCallsign(to))
}

func stripAPRSMessageID(text string) string {
	body, _ := splitAPRSMessageID(text)
	return body
}

func splitAPRSMessageID(text string) (string, string) {
	text = strings.TrimSpace(text)
	idx := strings.LastIndex(text, "{")
	if idx < 0 {
		return text, ""
	}
	id := text[idx+1:]
	if id == "" || len([]rune(id)) > 5 {
		return text, ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			if r < 'A' || r > 'Z' {
				if r < 'a' || r > 'z' {
					return text, ""
				}
			}
		}
	}
	return strings.TrimSpace(text[:idx]), normalizeAPRSMessageID(id)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (a *app) logAPRSReceiver(format string, args ...any) {
	text := fmt.Sprintf("%s %s\n", now(), fmt.Sprintf(format, args...))
	appendLogFile(a.cfg.aprsLogFile, text)
}
