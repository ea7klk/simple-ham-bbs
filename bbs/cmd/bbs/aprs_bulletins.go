package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// APRS bulletins.
//
// A bulletin is a message-format packet whose addressee is `BLNn` plus a
// five-character group, space-padded to the nine-character address field:
//
//	:BLN1NWS  :Tornado warning until 21:00Z for the metro area
//	 ^^^ ^^^^
//	 slot group
//
// They address a group rather than a callsign, so the user-facing receive path
// drops them: there is no account called BLN1NWS. This file gives them their own
// global store instead, deduplicated by content because senders repeat the same
// bulletin every few minutes.

const (
	// bulletinRetention is how long a bulletin stays readable, measured from the
	// last time it was heard so one still being transmitted does not expire.
	bulletinRetention = 10 * 24 * time.Hour
	// bulletinRowCap is a backstop against a pathological stream: the retention
	// sweep should keep the table small, but a flood inside one window would not
	// be caught by it.
	bulletinRowCap = 5000
	// bulletinAddressLen is the fixed length of the APRS message addressee field.
	bulletinAddressLen = 9
)

// bulletinCategories lists the categories in display order.
//
// The order is deliberate: what a reader most needs first — weather and
// emergencies — then coordination, then interest. `other` is last because it is
// the fallback for a group we do not know.
var bulletinCategories = []struct {
	ID   string
	Desc string
}{
	{"weather", "Watches, warnings, advisories and spotter activation"},
	{"emergency", "Declared emergencies, evacuations and served-agency coordination"},
	{"traffic", "National Traffic System and formal radiogram traffic"},
	{"nets", "Net times, frequencies and control stations"},
	{"events", "Field days, contests and special-event stations"},
	{"dx", "DXpeditions, band openings and propagation forecasts"},
	{"satellite", "Satellite passes, transponder status and ISS operations"},
	{"network", "APRS network announcements and igate or digipeater service notes"},
	{"service", "Repeater, gateway and infrastructure disruption"},
	{"organisation", "Society bulletins, rule changes and membership notices"},
	{"test", "Test bulletins"},
	{"general", "Uncategorised bulletins"},
	{"other", "A group this BBS does not recognise"},
}

// bulletinGroups maps an observed group to a category. Values are the bulletin
// category ids above; an unrecognised group falls through to the content
// heuristic and then to `other`.
var bulletinGroups = map[string]string{
	"NWS":   "weather",
	"WX":    "weather",
	"SKY":   "weather",
	"SKYW":  "weather",
	"EMERG": "emergency",
	"EMER":  "emergency",
	"ARES":  "emergency",
	"RACES": "emergency",
	"NTS":   "traffic",
	"TRAFF": "traffic",
	"RADIO": "traffic",
	"NET":   "nets",
	"SCHED": "nets",
	"EVENT": "events",
	"CONTE": "events",
	"FIELD": "events",
	"FD":    "events",
	"DX":    "dx",
	"PROP":  "dx",
	"SPOT":  "dx",
	"SAT":   "satellite",
	"AMSAT": "satellite",
	"ISS":   "satellite",
	"APRS":  "network",
	"IGATE": "network",
	"DIGI":  "network",
	"OUTAG": "service",
	"MAINT": "service",
	"SERV":  "service",
	"ARRL":  "organisation",
	"IARU":  "organisation",
	"TEST":  "test",
}

// bulletinKeywords is the content fallback, used only when the group is empty or
// unknown. Order matters: an emergency word outranks a weather word, because a
// bulletin mentioning both is an emergency bulletin.
var bulletinKeywords = []struct {
	Category string
	Words    []string
}{
	{"emergency", []string{"EMERGENCY", "EVACUAT", "MAYDAY", "LIFE THREAT", "IMMEDIATE EVACUATION"}},
	{"weather", []string{"WARNING", "WATCH", "ADVISORY", "TORNADO", "THUNDERSTORM", "FLOOD", "HURRICANE", "TYPHOON", "BLIZZARD", "WIND CHILL", "HEAT ADVISORY", "SPOTTER"}},
	{"satellite", []string{"SATELLITE", "TRANSPONDER", "ISS ", "AO-", "SO-", "FO-", "PASS "}},
	{"traffic", []string{"RADIOGRAM", "TRAFFIC", "NTS "}},
	{"nets", []string{"NET ", "NETS ", "CHECK-IN", "CHECKIN", "CONTROL STATION"}},
	{"events", []string{"CONTEST", "FIELD DAY", "SPECIAL EVENT", "EXPEDITION"}},
	{"service", []string{"OUTAGE", "MAINTENANCE", "OFF AIR", "DOWN FOR"}},
}

// bulletinAddress is a parsed addressee: the slot digit and the group.
type bulletinAddress struct {
	Slot  int
	Group string
}

// parseBulletinAddress reports whether an APRS message addressee is a bulletin
// address, and returns the slot and group if so.
//
// The field is `BLNn` followed by up to five group characters, space padded. A
// bare `BLNn` with no group is valid — it simply has no group — and so is a
// group shorter than five characters.
func parseBulletinAddress(to string) (bulletinAddress, bool) {
	to = strings.ToUpper(strings.TrimSpace(to))
	if len(to) < 4 || !strings.HasPrefix(to, "BLN") {
		return bulletinAddress{}, false
	}
	if to[3] < '0' || to[3] > '9' {
		return bulletinAddress{}, false
	}
	slot, err := strconv.Atoi(string(to[3]))
	if err != nil {
		return bulletinAddress{}, false
	}
	group := strings.ToUpper(strings.TrimSpace(to[4:]))
	// A group is at most five characters; anything longer is not a bulletin
	// address with extra padding, it is a different addressee.
	if len(group) > 5 {
		return bulletinAddress{}, false
	}
	return bulletinAddress{Slot: slot, Group: group}, true
}

// bulletinCategory resolves a category from the declared group, then content.
func bulletinCategory(group, text string) string {
	if category, ok := bulletinGroups[strings.ToUpper(strings.TrimSpace(group))]; ok {
		return category
	}
	upper := strings.ToUpper(text)
	for _, rule := range bulletinKeywords {
		for _, word := range rule.Words {
			if strings.Contains(upper, word) {
				return rule.Category
			}
		}
	}
	if strings.TrimSpace(group) == "" {
		// No group and no recognisable content: an ordinary uncategorised
		// bulletin, not an unknown one.
		return "general"
	}
	// A group was declared but this BBS does not know it. Keep it visible with
	// the raw group shown rather than pretending it is general.
	return "other"
}

// bulletinDedupeKey identifies a bulletin by its content, so a repeat updates the
// existing row instead of adding another.
//
// Text is normalised through the same sanitiser used for storage, so two packets
// that differ only in control characters or line endings collapse together.
func bulletinDedupeKey(from, group, text string) string {
	normalised := strings.Join(strings.Fields(sanitizeSingleLineText(text)), " ")
	sum := sha256.Sum256([]byte(normalizeAPRSCallsign(from) + "|" + strings.ToUpper(group) + "|" + normalised))
	return hex.EncodeToString(sum[:])
}

// storeAPRSBulletin records a bulletin, or updates the existing row when the same
// bulletin is heard again.
func (a *app) storeAPRSBulletin(msg receivedAPRS, addr bulletinAddress) (uint, bool, error) {
	text := sanitizeMultilineText(stripAPRSMessageID(msg.Text))
	if text == "" {
		return 0, false, nil
	}
	from := normalizeAPRSCallsign(msg.From)
	category := bulletinCategory(addr.Group, text)
	key := bulletinDedupeKey(from, addr.Group, text)
	at := msg.At
	if strings.TrimSpace(at) == "" {
		at = now()
	}

	var id uint
	created := false
	err := a.db.Transaction(func(tx *gorm.DB) error {
		existing := dbAPRSBulletin{}
		err := tx.Where("dedupe_key = ?", key).Take(&existing).Error
		if err == nil {
			id = existing.ID
			return tx.Model(&dbAPRSBulletin{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"last_seen":  at,
				"seen_count": gorm.Expr("seen_count + 1"),
			}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row := dbAPRSBulletin{
			DedupeKey: key,
			FirstSeen: at,
			LastSeen:  at,
			SeenCount: 1,
			From:      from,
			Slot:      addr.Slot,
			Group:     sanitizeSingleLineText(addr.Group),
			Category:  category,
			Text:      text,
			Raw:       sanitizeSingleLineText(msg.Raw),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		id = row.ID
		created = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if created && category == "other" {
		// An unrecognised group is worth seeing once: the taxonomy should grow
		// from observed traffic rather than guesses.
		a.logBBSAction("", "aprs_bulletin_unknown_group", "group=%q from=%q", addr.Group, from)
	}
	return id, created, nil
}

// loadAPRSBulletins returns bulletins newer than the retention window, newest
// first, optionally restricted to one category.
func (a *app) loadAPRSBulletins(category string, at time.Time) ([]dbAPRSBulletin, error) {
	cutoff := at.Add(-bulletinRetention).Format("2006-01-02 15:04 UTC")
	query := a.db.Where("last_seen >= ?", cutoff)
	if category != "" {
		query = query.Where("category = ?", category)
	}
	rows := []dbAPRSBulletin{}
	if err := query.Order("last_seen DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	// Sanitise on read as well as on write: a row written by an older version,
	// or by the web BBS, must not reach a terminal unfiltered.
	for i := range rows {
		rows[i].Text = sanitizeMultilineText(rows[i].Text)
		rows[i].From = sanitizeSingleLineText(rows[i].From)
		rows[i].Group = sanitizeSingleLineText(rows[i].Group)
		rows[i].Raw = sanitizeSingleLineText(rows[i].Raw)
	}
	return rows, nil
}

// bulletinCounts returns how many live bulletins each category holds.
func (a *app) bulletinCounts(at time.Time) (map[string]int, error) {
	cutoff := at.Add(-bulletinRetention).Format("2006-01-02 15:04 UTC")
	type row struct {
		Category string
		Count    int
	}
	rows := []row{}
	if err := a.db.Model(&dbAPRSBulletin{}).
		Select("category, count(*) as count").
		Where("last_seen >= ?", cutoff).
		Group("category").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Category] = r.Count
	}
	return out, nil
}

// pruneAPRSBulletins drops bulletins past the retention window and enforces the
// row cap. It runs with the other nightly housekeeping.
func (a *app) pruneAPRSBulletins(at time.Time) error {
	cutoff := at.Add(-bulletinRetention).Format("2006-01-02 15:04 UTC")
	if err := a.db.Where("last_seen < ?", cutoff).Delete(&dbAPRSBulletin{}).Error; err != nil {
		return err
	}

	var total int64
	if err := a.db.Model(&dbAPRSBulletin{}).Count(&total).Error; err != nil {
		return err
	}
	if int(total) <= bulletinRowCap {
		return nil
	}
	// Over the cap: drop the oldest by last_seen down to the cap.
	excess := int(total) - bulletinRowCap
	rows := []dbAPRSBulletin{}
	if err := a.db.Select("id").Order("last_seen ASC, id ASC").Limit(excess).Find(&rows).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	return a.db.Delete(&dbAPRSBulletin{}, "id IN ?", ids).Error
}

// bulletinAge renders a bulletin timestamp as a short relative age for a list.
func bulletinAge(stamp string, at time.Time) string {
	when, err := time.Parse("2006-01-02 15:04 MST", stamp)
	if err != nil {
		when, err = time.Parse("2006-01-02 15:04 UTC", stamp)
		if err != nil {
			return ""
		}
	}
	d := at.Sub(when)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
