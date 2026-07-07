package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/pbkdf2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *app) loadUsers() (map[string]userProfile, error) {
	users := map[string]userProfile{}
	err := readJSON(a.cfg.usersFile, &users, map[string]userProfile{})
	normalized := map[string]userProfile{}
	for key, profile := range users {
		upper := normalizeCallsign(key)
		if upper == "" {
			continue
		}
		if existing, ok := normalized[upper]; ok {
			if existing.PasswordHash == "" && profile.PasswordHash != "" {
				normalized[upper] = profile
			}
		} else {
			normalized[upper] = profile
		}
	}
	return normalized, err
}

func (a *app) saveUsers(users map[string]userProfile) error {
	return writeJSON(a.cfg.usersFile, users)
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

func writeJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
