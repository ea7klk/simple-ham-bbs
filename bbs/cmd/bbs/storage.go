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

// applyConfiguredSysops flags accounts listed in BBS_SYSOPS and returns the
// callsigns it changed, so the caller can persist exactly those rows.
func (a *app) applyConfiguredSysops(users map[string]userProfile) []string {
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

func (a *app) isSysop(callsign string, profile userProfile) bool {
	return a.cfg.sysops[callsign] || profile.IsSysop
}

func (a *app) wouldRemoveLastSysop(users map[string]userProfile, callsign string) bool {
	profile := users[callsign]
	if !a.isSysop(callsign, profile) || profile.Disabled {
		return false
	}
	count := 0
	for key, p := range users {
		if a.isSysop(key, p) && !p.Disabled {
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
		return nil
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
