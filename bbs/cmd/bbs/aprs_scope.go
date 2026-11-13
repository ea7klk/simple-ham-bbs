package main

import (
	"fmt"
	"strings"
)

// APRS message sections.
//
// The first version of this split classified by peer callsign alone: anything
// from ANSRVR was "#APRSThursday". Production traffic showed that to be wrong on
// the larger half of the data.
//
// ANSRVR is the *group server*, not a Thursday-specific bot. It carries AARN,
// NEAPRS and every other group this station has joined, so its traffic cannot be
// assumed to be Thursday. And the actual #APRSThursday conversation does not come
// from ANSRVR at all: it is relayed as ordinary messages from the participants,
// each tagged with the group in the body —
//
//	N:HOTG happy aprsthursday!!!          from KB3KBR-2
//	N:HOTG Saludos desde Republica Dom.   from HI3RRR-7
//
// Measured on the live database: 478 messages carry the N:HOTG tag, from 413
// distinct stations, and exactly 6 of them came from ANSRVR. Classifying by peer
// put all 472 in "other stations" — the Thursday section was nearly empty while
// the net itself sat elsewhere.
//
// So the rule is by content first, peer second:
//
//  1. An explicit group tag in the body decides. "N:HOTG …" is Thursday; any
//     other "N:<GROUP> …" belongs to the group server, not to Thursday.
//  2. Failing that, the peer decides: APRSPH is the net bot, ANSRVR is the group
//     server.
//  3. ANSRVR traffic that mentions HOTG but carries no tag (our own "CQ HOTG")
//     is Thursday.
//
// An acknowledgement ("ack1B}") names no group, so it lands with the group
// server rather than being guessed into Thursday. That is the honest answer:
// better an acknowledgement in a general bucket than one filed under a net it may
// not belong to.

// aprsScope names one section of the APRS history.
type aprsScope string

const (
	// aprsScopeDirect is traffic with another station that is not group traffic.
	aprsScopeDirect aprsScope = "direct"
	// aprsScopeAPRSPH is the #APRSPH net bot.
	aprsScopeAPRSPH aprsScope = "aprsph"
	// aprsScopeThursday is the #APRSThursday net: group traffic tagged HOTG,
	// whichever station relayed it.
	aprsScopeThursday aprsScope = "thursday"
	// aprsScopeGroups is the rest of the group server's traffic — other groups,
	// and acknowledgements that name no group at all.
	aprsScopeGroups aprsScope = "groups"
)

// aprsScopes is the display order: what most callers want first, and the vague
// bucket last.
var aprsScopes = []aprsScope{
	aprsScopeDirect, aprsScopeAPRSPH, aprsScopeThursday, aprsScopeGroups,
}

// aprsThursdayGroup is the ANSRVR group name for #APRSThursday.
const aprsThursdayGroup = "HOTG"

// aprsGroupTagMaxLen bounds a group name in an "N:<GROUP>" tag. Group names are
// short in practice — HOTG, AARN, NEAPRS — but this is a shape check, not a
// membership test: a longer token is not a group tag.
const aprsGroupTagMaxLen = 8

// aprsGroupTag extracts the group from a group-relayed body.
//
// ANSRVR and the clients that relay it prefix group traffic with the group name:
//
//	N:HOTG 24 Messages Sent
//	N:AARN Check in
//
// The tag is "N:", one to eight characters, then a space or the end. The bound
// is generous because group names in the wild are not: "NEAPRS" is six, and an
// earlier five-character limit silently pushed its traffic into "direct".
// Anything that does not match the shape is not a tag, so ordinary text
// beginning with "N:" cannot masquerade as one.
func aprsGroupTag(text string) (string, bool) {
	body := strings.TrimSpace(text)
	upper := strings.ToUpper(body)
	rest, ok := strings.CutPrefix(upper, "N:")
	if !ok {
		return "", false
	}
	end := strings.IndexAny(rest, " {")
	if end < 0 {
		end = len(rest)
	}
	group := strings.TrimSpace(rest[:end])
	if group == "" || len(group) > aprsGroupTagMaxLen {
		return "", false
	}
	return group, true
}

// aprsMentionsThursday reports whether a body names the #APRSThursday group or
// net, which is how our own outgoing commands identify it: "CQ HOTG", "K HOTG",
// "U HOTG".
func aprsMentionsThursday(text string) bool {
	upper := strings.ToUpper(text)
	if strings.Contains(upper, aprsThursdayGroup) {
		return true
	}
	return strings.Contains(upper, "APRSTHURSDAY")
}

// aprsScopeOfMessage classifies one stored message.
//
// peer is the other end of the conversation — the sender of a received message,
// the addressee of a sent one — and text is the body as stored.
func aprsScopeOfMessage(peer, text string) aprsScope {
	if group, ok := aprsGroupTag(text); ok {
		if group == aprsThursdayGroup {
			return aprsScopeThursday
		}
		// A different group on the group server. Not Thursday, and not a
		// conversation with a station either.
		return aprsScopeGroups
	}
	switch aprsBaseCallsign(peer) {
	case aprsBotAPRSPH:
		return aprsScopeAPRSPH
	case aprsBotANSRVR:
		// The group server's own messages: join and leave confirmations, and
		// acknowledgements. Ours are Thursday commands; anything else here names
		// no group, so it stays with the group server.
		if aprsMentionsThursday(text) {
			return aprsScopeThursday
		}
		return aprsScopeGroups
	}
	return aprsScopeDirect
}

// labelKey is the translation key for a section's label.
func (s aprsScope) labelKey() string {
	switch s {
	case aprsScopeAPRSPH:
		return "aprs_section_aprsph"
	case aprsScopeThursday:
		return "aprs_section_thursday"
	case aprsScopeGroups:
		return "aprs_section_groups"
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
	case aprsScopeGroups:
		return "aprs_section_groups_desc"
	}
	return "aprs_section_direct_desc"
}

// filterReceivedByScope keeps the messages in one section, preserving order.
func filterReceivedByScope(rows []receivedAPRS, scope aprsScope) []receivedAPRS {
	out := make([]receivedAPRS, 0, len(rows))
	for _, row := range rows {
		if aprsScopeOfMessage(row.From, row.Text) == scope {
			out = append(out, row)
		}
	}
	return out
}

// filterSentByScope keeps the messages in one section, preserving order.
func filterSentByScope(rows []sentAPRS, scope aprsScope) []sentAPRS {
	out := make([]sentAPRS, 0, len(rows))
	for _, row := range rows {
		if aprsScopeOfMessage(row.To, row.Text) == scope {
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
		counts[aprsScopeOfMessage(row.From, row.Text)]++
	}
	return counts
}

// countSentScopes counts each section of the sent history.
func countSentScopes(rows []sentAPRS) map[aprsScope]int {
	counts := map[aprsScope]int{}
	for _, row := range rows {
		counts[aprsScopeOfMessage(row.To, row.Text)]++
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
