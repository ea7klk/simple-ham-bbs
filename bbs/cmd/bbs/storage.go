package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
	"gorm.io/gorm"
)

func (a *app) loadUsers() (map[string]userProfile, error) {
	rows := []dbUser{}
	err := a.db.Order("callsign").Find(&rows).Error
	users := map[string]userProfile{}
	for _, row := range rows {
		upper := normalizeCallsign(row.Callsign)
		if upper == "" {
			continue
		}
		users[upper] = profileFromRow(row)
	}
	return users, err
}

func profileFromRow(row dbUser) userProfile {
	return userProfile{
		FullName:     sanitizeSingleLineText(row.FullName),
		Email:        sanitizeSingleLineText(row.Email),
		Maidenhead:   sanitizeSingleLineText(row.Maidenhead),
		Language:     row.Language,
		EnableAPRS:   row.EnableAPRS,
		QTH:          sanitizeSingleLineText(row.QTH),
		Rig:          sanitizeSingleLineText(row.Rig),
		PasswordHash: row.PasswordHash,
		IsSysop:      row.IsSysop,
		Disabled:     row.Disabled,
		FirstSeen:    row.FirstSeen,
		LastSeen:     row.LastSeen,
		Registered:   row.Registered,
	}
}

func (a *app) loadUser(callsign string) (userProfile, bool, error) {
	key := normalizeCallsign(callsign)
	if key == "" {
		return userProfile{}, false, nil
	}
	row := dbUser{}
	err := a.db.Where("callsign = ?", key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return userProfile{}, false, nil
	}
	if err != nil {
		return userProfile{}, false, err
	}
	return profileFromRow(row), true, nil
}

// hasActiveSysop reports whether any enabled account already holds the sysop
// role.
func hasActiveSysop(users map[string]userProfile) bool {
	for _, profile := range users {
		if profile.IsSysop && !profile.Disabled {
			return true
		}
	}
	return false
}

// storedActiveSysops counts enabled accounts that hold the sysop role in the
// database, rather than in whatever snapshot the caller is holding.
func (a *app) storedActiveSysops() (int64, error) {
	var count int64
	err := a.db.Model(&dbUser{}).Where("is_sysop = ? AND disabled = ?", true, false).Count(&count).Error
	return count, err
}

// applyConfiguredSysops promotes accounts listed in BBS_SYSOPS and returns the
// callsigns it changed, so the caller can persist exactly those rows.
//
// BBS_SYSOPS is a bootstrap mechanism, not a standing grant: once the BBS has
// any active sysop, the configured list confers nothing. Without that rule a
// configured callsign stayed permanently privileged, so anyone who registered
// it (callsigns cannot be verified) inherited the sysop menu forever.
func (a *app) applyConfiguredSysops(users map[string]userProfile) []string {
	if hasActiveSysop(users) {
		return nil
	}
	// The map was read before this session's first screen, so another session may
	// have bootstrapped in the meantime. Confirm against the database, and treat
	// an unreadable count as "a sysop exists": never grant on doubt.
	if count, err := a.storedActiveSysops(); err != nil || count > 0 {
		return nil
	}
	changed := []string{}
	for callsign := range a.cfg.sysops {
		if profile, ok := users[callsign]; ok && !profile.IsSysop {
			profile.IsSysop = true
			users[callsign] = profile
			changed = append(changed, callsign)
		}
	}
	sort.Strings(changed)
	return changed
}

// isBootstrapSysop reports whether callsign may be granted sysop because it is
// listed in BBS_SYSOPS and the deployment has no active sysop yet. It fails
// closed: if the count cannot be read, no role is granted.
func (a *app) isBootstrapSysop(callsign string) bool {
	if !a.cfg.sysops[normalizeCallsign(callsign)] {
		return false
	}
	count, err := a.storedActiveSysops()
	if err != nil {
		return false
	}
	return count == 0
}

// isSysop answers from the stored profile alone. BBS_SYSOPS is deliberately not
// consulted here: it is used once, to bootstrap the first sysop, and has no
// authority afterwards.
func (a *app) isSysop(profile userProfile) bool {
	return profile.IsSysop
}

func (a *app) wouldRemoveLastSysop(users map[string]userProfile, callsign string) bool {
	profile := users[callsign]
	if !a.isSysop(profile) || profile.Disabled {
		return false
	}
	count := 0
	for _, p := range users {
		if a.isSysop(p) && !p.Disabled {
			count++
		}
	}
	return count <= 1
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	digest := pbkdf2.Key([]byte(password), salt, passwordIterations, 32, sha256.New)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", passwordIterations, base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(digest))
}

func verifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	actual := pbkdf2.Key([]byte(password), salt, iter, len(expected), sha256.New)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// readJSON loads path into target, falling back when the file is missing.
//
// A malformed file still falls back — a BBS that refuses to start because one
// JSON file is corrupt is worse than one that runs with raw translation keys —
// but the parse error is returned so the caller can log it. Returning nil there
// is what left a corrupt translations.json looking like a working install with
// a UI full of raw keys.
func readJSON[T any](path string, target *T, fallback T) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		*target = fallback
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		*target = fallback
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
