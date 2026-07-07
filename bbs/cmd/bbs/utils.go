package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func now() string {
	return time.Now().UTC().Format("2006-01-02 15:04 UTC")
}

func normalizeCallsign(callsign string) string {
	return strings.ToUpper(strings.TrimSpace(callsign))
}

func normalizeLocator(locator string) string {
	locator = strings.TrimSpace(locator)
	if len(locator) < 2 {
		return strings.ToUpper(locator)
	}
	return strings.ToUpper(locator[:2]) + locator[2:]
}

func profileComplete(p userProfile) bool {
	return p.FullName != "" && p.Email != "" && p.Language != "" && p.PasswordHash != ""
}

func profileRows(a *app, lang string, p userProfile) [][]string {
	return [][]string{{a.t(lang, "full_name"), p.FullName}, {a.t(lang, "email"), p.Email}, {a.t(lang, "maidenhead"), p.Maidenhead}, {a.t(lang, "language"), languages[p.Language]}, {a.t(lang, "enable_aprs"), boolString(p.EnableAPRS)}, {a.t(lang, "qth"), p.QTH}, {a.t(lang, "rig"), p.Rig}}
}

func languageOptions() []option {
	out := []option{}
	for _, code := range languageOrder {
		out = append(out, option{code, languages[code] + " (" + code + ")"})
	}
	return out
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func choiceStep(choices []option, current string, delta int) string {
	if len(choices) == 0 {
		return current
	}
	idx := 0
	for i, choice := range choices {
		if choice.value == current {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(choices)) % len(choices)
	return choices[idx].value
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func buttonLabelKey(button string) string {
	switch button {
	case "login":
		return "login_button"
	case "save":
		return "save_button"
	case "delete":
		return "delete_button"
	case "reply":
		return "reply_button"
	case "send":
		return "send_button"
	default:
		return "cancel_button"
	}
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boardID(name string) string {
	id := strings.Trim(boardIDRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if id == "" {
		return defaultBoardID
	}
	if len(id) > 40 {
		return id[:40]
	}
	return id
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if strings.TrimSpace(fmt.Sprint(value)) != "" && fmt.Sprint(value) != "<nil>" {
			return value
		}
	}
	return ""
}

func mapToMessage(raw any) (message, bool) {
	item, ok := raw.(map[string]any)
	if !ok {
		return message{}, false
	}
	replies := []message{}
	if rawReplies, ok := item["replies"].([]any); ok {
		for _, rawReply := range rawReplies {
			if reply, ok := mapToMessage(rawReply); ok {
				replies = append(replies, reply)
			}
		}
	}
	return message{From: fmt.Sprint(item["from"]), Subject: fmt.Sprint(item["subject"]), Body: fmt.Sprint(item["body"]), Created: fmt.Sprint(item["created"]), Edited: fmt.Sprint(item["edited"]), Replies: replies}, true
}

func reverseSent(items []sentAPRS) []sentAPRS {
	out := make([]sentAPRS, len(items))
	for i := range items {
		out[i] = items[len(items)-1-i]
	}
	return out
}

func asciiSafe(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 32 && r <= 126 {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}
