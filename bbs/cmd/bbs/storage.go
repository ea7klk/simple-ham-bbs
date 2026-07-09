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
		users[upper] = userProfile{
			FullName:     row.FullName,
			Email:        row.Email,
			Maidenhead:   row.Maidenhead,
			Language:     row.Language,
			EnableAPRS:   row.EnableAPRS,
			QTH:          row.QTH,
			Rig:          row.Rig,
			PasswordHash: row.PasswordHash,
			IsSysop:      row.IsSysop,
			Disabled:     row.Disabled,
			FirstSeen:    row.FirstSeen,
			LastSeen:     row.LastSeen,
		}
	}
	return users, err
}

func (a *app) saveUsers(users map[string]userProfile) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		var existing []dbUser
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		keep := map[string]bool{}
		for key, profile := range users {
			callsign := normalizeCallsign(key)
			if callsign == "" {
				continue
			}
			keep[callsign] = true
			row := dbUser{
				Callsign:     callsign,
				FullName:     profile.FullName,
				Email:        profile.Email,
				Maidenhead:   profile.Maidenhead,
				Language:     profile.Language,
				EnableAPRS:   profile.EnableAPRS,
				QTH:          profile.QTH,
				Rig:          profile.Rig,
				PasswordHash: profile.PasswordHash,
				IsSysop:      profile.IsSysop,
				Disabled:     profile.Disabled,
				FirstSeen:    profile.FirstSeen,
				LastSeen:     profile.LastSeen,
			}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		for _, row := range existing {
			if !keep[row.Callsign] {
				if err := tx.Delete(&dbUser{}, "callsign = ?", row.Callsign).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (a *app) applyConfiguredSysops(users map[string]userProfile) bool {
	changed := false
	for callsign := range a.cfg.sysops {
		if profile, ok := users[callsign]; ok && !profile.IsSysop {
			profile.IsSysop = true
			users[callsign] = profile
			changed = true
		}
	}
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
