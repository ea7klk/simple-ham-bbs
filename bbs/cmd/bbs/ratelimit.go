package main

import (
	"errors"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Login throttling (M1).
//
// fail2ban used to watch the sshd log, which only saw SSH-layer events. The BBS
// password prompt lives inside the application, so those attempts were invisible
// to it, and with guest mode enabled there is no SSH password to guess at all.
// The counter that did exist lived in a local map for the duration of one
// session, so opening a new connection reset it.
//
// This throttle is stored in SQLite, which makes it shared by every bbs_app
// process, and keyed by the client address taken from SSH_CONNECTION, so it
// cannot be reset by reconnecting.

const (
	// authFailureWindow is the period over which failures accumulate.
	authFailureWindow = 15 * time.Minute
	// authFailureLimit is the number of failed attempts within that window
	// before the address is locked out.
	authFailureLimit = 5
	// authLockoutDuration is how long a locked address must wait.
	authLockoutDuration = 15 * time.Minute
	// authFailureRetention is how long an idle counter row is kept.
	authFailureRetention = 24 * time.Hour
)

// dbAuthFailure is one client address's failure state.
type dbAuthFailure struct {
	Address     string `gorm:"primaryKey;size:64"`
	Failures    int
	WindowStart int64
	LockedUntil int64
}

var errAuthLocked = errors.New("too many failed attempts from this address")

// clientAddress returns the address the SSH connection came from, as reported by
// sshd in SSH_CONNECTION ("client_ip client_port server_ip server_port").
//
// When the variable is absent (a local test, or a session started outside sshd)
// every such caller shares one bucket rather than escaping the limit.
func clientAddress() string {
	raw := strings.TrimSpace(os.Getenv("SSH_CONNECTION"))
	if raw == "" {
		return "unknown"
	}
	fields := strings.Fields(raw)
	address := strings.TrimSpace(fields[0])
	if address == "" || len(address) > 64 {
		return "unknown"
	}
	return address
}

// authLockedRemaining reports how much longer address is locked out, or zero.
func (a *app) authLockedRemaining(address string, at time.Time) (time.Duration, error) {
	row := dbAuthFailure{}
	err := a.db.Where("address = ?", address).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if row.LockedUntil == 0 {
		return 0, nil
	}
	remaining := time.Unix(row.LockedUntil, 0).Sub(at)
	if remaining <= 0 {
		return 0, nil
	}
	return remaining, nil
}

// recordAuthFailure counts one failed attempt and locks the address once the
// window limit is reached. It returns whether the address is now locked.
func (a *app) recordAuthFailure(address string, at time.Time) (bool, error) {
	locked := false
	err := a.db.Transaction(func(tx *gorm.DB) error {
		row := dbAuthFailure{}
		err := tx.Where("address = ?", address).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = dbAuthFailure{Address: address}
		}
		windowStart := time.Unix(row.WindowStart, 0)
		if row.WindowStart == 0 || at.Sub(windowStart) > authFailureWindow {
			row.Failures = 0
			row.WindowStart = at.Unix()
		}
		row.Failures++
		if row.Failures >= authFailureLimit {
			row.LockedUntil = at.Add(authLockoutDuration).Unix()
			row.Failures = 0
			row.WindowStart = at.Unix()
			locked = true
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "address"}},
			UpdateAll: true,
		}).Create(&row).Error
	})
	return locked, err
}

// clearAuthFailures forgets an address's failures after a successful login.
func (a *app) clearAuthFailures(address string) error {
	return a.db.Delete(&dbAuthFailure{}, "address = ?", address).Error
}

// pruneAuthFailures drops counters that have been idle past their retention, so
// the table cannot grow without bound.
func (a *app) pruneAuthFailures(at time.Time) error {
	cutoff := at.Add(-authFailureRetention).Unix()
	return a.db.Where("window_start < ? AND locked_until < ?", cutoff, at.Unix()).Delete(&dbAuthFailure{}).Error
}

// Registration throttling (L2).
//
// Creating an account costs a 200k-iteration PBKDF2 hash, and nothing limited
// how many accounts one caller could create. The limit is generous enough for a
// club station where several operators share an address, and small enough that
// walking the address space to burn CPU is not worthwhile.

const (
	// registrationWindow is the period over which new accounts are counted.
	registrationWindow = time.Hour
	// registrationLimit is how many accounts one address may create per window.
	registrationLimit = 5
	// registrationRetention is how long an idle counter row is kept.
	registrationRetention = 24 * time.Hour
)

// dbRegistration counts new accounts created from one client address.
type dbRegistration struct {
	Address     string `gorm:"primaryKey;size:64"`
	Count       int
	WindowStart int64
}

// registrationAllowed reports whether address may create another account now.
//
// It does not consume a slot: a caller that opens the registration form and
// cancels must not be charged. The count is incremented by
// recordRegistration once an account actually exists. The check-then-record gap
// can therefore be crossed twice by simultaneous registrations from one
// address, which overshoots the limit by at most the number of concurrent
// sessions — bounded, and harmless for a limit that exists to stop bulk
// creation.
func (a *app) registrationAllowed(address string, at time.Time) (bool, error) {
	row := dbRegistration{}
	err := a.db.Where("address = ?", address).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if at.Sub(time.Unix(row.WindowStart, 0)) > registrationWindow {
		return true, nil
	}
	return row.Count < registrationLimit, nil
}

// recordRegistration counts one created account against address.
func (a *app) recordRegistration(address string, at time.Time) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		row := dbRegistration{}
		err := tx.Where("address = ?", address).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || at.Sub(time.Unix(row.WindowStart, 0)) > registrationWindow {
			row = dbRegistration{Address: address, WindowStart: at.Unix()}
		}
		row.Count++
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "address"}},
			UpdateAll: true,
		}).Create(&row).Error
	})
}

// pruneRegistrations drops counters that have been idle past their retention.
func (a *app) pruneRegistrations(at time.Time) error {
	cutoff := at.Add(-registrationRetention).Unix()
	return a.db.Where("window_start < ?", cutoff).Delete(&dbRegistration{}).Error
}
