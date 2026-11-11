package main

import (
	"fmt"
	"strings"
)

// APRS message sections.
//
// Bot traffic used to sit in the same list as messages from other operators, so
// somebody looking for a reply from a station had to page past net check-ins,
// acknowledgements and group notices to find it. The two bots are identifiable
// from the stored row alone — the peer *is* the bot: a sent row addresses it, a
// received row comes from it. No column is needed and nothing has to be
// recomputed when a message is stored.
//
// The split is by peer rather than by content. An acknowledgement from ANSRVR
// ("ack1B}") does not name the group, so a content rule would file it with the
// wrong traffic and hide it from the message it acks.

// aprsScope names one section of the APRS history.
type aprsScope string

const (
	// aprsScopeDirect is traffic with another station: everything that is not
	// addressed to, or sent by, a bot.
	aprsScopeDirect aprsScope = "direct"
	// aprsScopeAPRSPH is the #APRSPH net bot.
	aprsScopeAPRSPH aprsScope = "aprsph"
	// aprsScopeThursday is the ANSRVR group server, which carries #APRSThursday
	// and any other group this station has joined.
	aprsScopeThursday aprsScope = "thursday"
)

// aprsScopes is the display order, direct first because it is what most callers
// are looking for.
var aprsScopes = []aprsScope{aprsScopeDirect, aprsScopeAPRSPH, aprsScopeThursday}

// aprsScopeOfPeer classifies a peer callsign-SSID.
//
// The SSID is ignored: the bots answer from their base callsign, but a station
// may address APRSPH-0 and an acknowledgement can arrive from ANSRVR with any
// SSID. Comparing the base callsign keeps those together with the bot they
// belong to rather than stranding them among direct traffic.
func aprsScopeOfPeer(peer string) aprsScope {
	base := strings.ToUpper(strings.TrimSpace(normalizeAPRSCallsign(peer)))
	if i := strings.Index(base, "-"); i > 0 {
		base = base[:i]
	}
	switch base {
	case aprsBotAPRSPH:
		return aprsScopeAPRSPH
	case aprsBotANSRVR:
		return aprsScopeThursday
	}
	return aprsScopeDirect
}

// scopeKey is the translation key for a section's label.
func (s aprsScope) labelKey() string {
	switch s {
	case aprsScopeAPRSPH:
		return "aprs_section_aprsph"
	case aprsScopeThursday:
		return "aprs_section_thursday"
	}
	return "aprs_section_direct"
}

// descriptionKey is the translation key for a section's one-line description.
func (s aprsScope) descriptionKey() string {
	switch s {
	case aprsScopeAPRSPH:
		return "aprs_section_aprsph_desc"
	case aprsScopeThursday:
		return "aprs_section_thursday_desc"
	}
	return "aprs_section_direct_desc"
}

// filterReceivedByScope keeps the messages in one section, preserving order.
func filterReceivedByScope(rows []receivedAPRS, scope aprsScope) []receivedAPRS {
	out := make([]receivedAPRS, 0, len(rows))
	for _, row := range rows {
		if aprsScopeOfPeer(row.From) == scope {
			out = append(out, row)
		}
	}
	return out
}

// filterSentByScope keeps the messages in one section, preserving order.
func filterSentByScope(rows []sentAPRS, scope aprsScope) []sentAPRS {
	out := make([]sentAPRS, 0, len(rows))
	for _, row := range rows {
		if aprsScopeOfPeer(row.To) == scope {
			out = append(out, row)
		}
	}
	return out
}

// countReceivedScopes counts each section, so the menu can show an empty one as
// empty rather than sending the caller into a list with nothing in it.
func countReceivedScopes(rows []receivedAPRS) map[aprsScope]int {
	counts := map[aprsScope]int{}
	for _, row := range rows {
		counts[aprsScopeOfPeer(row.From)]++
	}
	return counts
}

// countSentScopes counts each section of the sent history.
func countSentScopes(rows []sentAPRS) map[aprsScope]int {
	counts := map[aprsScope]int{}
	for _, row := range rows {
		counts[aprsScopeOfPeer(row.To)]++
	}
	return counts
}

// aprsSectionLabel renders "label (count)" for the section menu.
func (a *app) aprsSectionLabel(lang string, scope aprsScope, count int) string {
	return fmt.Sprintf("%s (%d)", a.t(lang, scope.labelKey()), count)
}

// aprsSectionMenu asks which section to open and reports the choice. A section
// with nothing in it is still offered, with a zero, so the shape of the history
// is visible at a glance instead of being discovered by opening each one.
func (a *app) aprsSectionMenu(lang, titleKey string, counts map[aprsScope]int) (aprsScope, bool) {
	opts := []option{}
	for i, scope := range aprsScopes {
		opts = append(opts, option{fmt.Sprint(i + 1), a.aprsSectionLabel(lang, scope, counts[scope])})
	}
	opts = append(opts, option{"q", a.t(lang, "back_button")})
	choice := a.runMenu(lang, a.t(lang, titleKey), a.t(lang, "aprs_section_prompt"), opts)
	idx, err := parseMenuIndex(choice, len(aprsScopes))
	if err != nil {
		return "", false
	}
	return aprsScopes[idx], true
}

// parseMenuIndex turns a one-based menu choice into a zero-based index, or
// reports that the caller backed out.
func parseMenuIndex(choice string, size int) (int, error) {
	if strings.TrimSpace(choice) == "q" || strings.TrimSpace(choice) == "" {
		return 0, fmt.Errorf("cancelled")
	}
	idx := 0
	if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil {
		return 0, err
	}
	idx--
	if idx < 0 || idx >= size {
		return 0, fmt.Errorf("choice out of range")
	}
	return idx, nil
}
