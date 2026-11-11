package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Account tokens: activation codes, password resets and email-change links.
//
// All of it is stored in the database the BBS already shares with the web BBS
// (`/var/lib/bbs`), so a tokenised link can be honoured by the web front end
// without the two services exchanging anything else. Nothing secret is stored:
// codes and tokens are held as SHA-256 hashes, so a copy of the database does not
// let anyone redeem a pending link.

const (
	// activationCodeLength is the number of characters in an activation code.
	activationCodeLength = 4
	// activationAlphabet deliberately omits I, O, 0 and 1: the code is read off
	// a screen and typed back, and those glyphs are the ones people transcribe
	// wrongly. It is still capitals plus digits.
	activationAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// activationCodeTTL is how long a code stays valid.
	activationCodeTTL = 24 * time.Hour
	// activationAttempts is how many wrong codes are accepted before the code is
	// discarded and a new one must be requested.
	activationAttempts = 5

	// passwordResetTTL is how long a password-reset link works.
	passwordResetTTL = 2 * time.Hour
	// emailChangeTTL is how long an email-change link works.
	emailChangeTTL = 24 * time.Hour
	// tokenBytes is the entropy in a link token.
	tokenBytes = 32
	// tokenRetention is how long a spent or expired token row is kept, so an
	// operator can still see that a reset happened.
	tokenRetention = 30 * 24 * time.Hour
	// resetRequestsPerHour caps how many reset mails one account can trigger.
	resetRequestsPerHour = 3
)

// Token purposes. The value is what the web BBS switches on.
const (
	tokenPurposePasswordReset = "password_reset"
	tokenPurposeEmailOld      = "email_change_old"
	tokenPurposeEmailNew      = "email_change_new"
)

var (
	errActivationExpired = errors.New("activation code expired")
	errActivationTooMany = errors.New("too many wrong activation codes")
)

// dbActivation holds the pending activation code for one account.
type dbActivation struct {
	Callsign  string `gorm:"primaryKey;size:32"`
	CodeHash  string `gorm:"size:64"`
	ExpiresAt int64
	Attempts  int
	CreatedAt int64
}

// dbToken is a redeemable link token.
//
// The web BBS resolves a link by hashing the presented token and matching
// token_hash, then checking expires_at and consumed_at. For the two email-change
// purposes it additionally needs change_id, which binds the pair together.
type dbToken struct {
	ID         uint   `gorm:"primaryKey"`
	TokenHash  string `gorm:"uniqueIndex;size:64"`
	Purpose    string `gorm:"index;size:32"`
	Callsign   string `gorm:"index;size:32"`
	Email      string `gorm:"size:160"`
	ChangeID   string `gorm:"index;size:40"`
	CreatedAt  int64
	ExpiresAt  int64
	ConsumedAt int64
}

// dbSetting is this application's own key/value store. It is separate from
// db_meta, which belongs to the web BBS.
type dbSetting struct {
	Key   string `gorm:"primaryKey;size:64"`
	Value string
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newToken returns a URL-safe token and its stored hash.
func newToken() (string, string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("token entropy: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

// newActivationCode returns a short human-typed code and its stored hash.
func newActivationCode(callsign string) (string, string, error) {
	limit := big.NewInt(int64(len(activationAlphabet)))
	var b strings.Builder
	for i := 0; i < activationCodeLength; i++ {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", "", fmt.Errorf("activation code entropy: %w", err)
		}
		b.WriteByte(activationAlphabet[n.Int64()])
	}
	code := b.String()
	return code, activationHash(callsign, code), nil
}

// activationHash binds a code to its callsign, so a code cannot be replayed
// against a different account even if two happened to collide.
func activationHash(callsign, code string) string {
	return hashToken(strings.ToUpper(callsign) + "|" + normalizeActivationCode(code))
}

func normalizeActivationCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	return code
}

// issueActivationCode replaces any pending code for callsign and returns the
// plaintext code to email.
func (a *app) issueActivationCode(callsign string, at time.Time) (string, error) {
	code, hash, err := newActivationCode(callsign)
	if err != nil {
		return "", err
	}
	row := dbActivation{
		Callsign:  normalizeCallsign(callsign),
		CodeHash:  hash,
		ExpiresAt: at.Add(activationCodeTTL).Unix(),
		CreatedAt: at.Unix(),
	}
	if err := a.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "callsign"}}, UpdateAll: true}).Create(&row).Error; err != nil {
		return "", err
	}
	return code, nil
}

// verifyActivationCode consumes one attempt against the pending code.
func (a *app) verifyActivationCode(callsign, code string, at time.Time) error {
	key := normalizeCallsign(callsign)
	row := dbActivation{}
	err := a.db.Where("callsign = ?", key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errActivationExpired
	}
	if err != nil {
		return err
	}
	if at.Unix() > row.ExpiresAt {
		_ = a.clearActivationCode(key)
		return errActivationExpired
	}
	if row.Attempts >= activationAttempts {
		_ = a.clearActivationCode(key)
		return errActivationTooMany
	}
	if activationHash(key, code) != row.CodeHash {
		// Count the failure before returning, so a brute-force loop burns its
		// attempts even if the caller ignores the error.
		if err := a.db.Model(&dbActivation{}).Where("callsign = ?", key).
			UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; err != nil {
			return err
		}
		return errors.New("wrong activation code")
	}
	return a.clearActivationCode(key)
}

func (a *app) clearActivationCode(callsign string) error {
	return a.db.Delete(&dbActivation{}, "callsign = ?", normalizeCallsign(callsign)).Error
}

// issueToken stores a link token and returns the plaintext to put in the URL.
func (a *app) issueToken(purpose, callsign, email, changeID string, ttl time.Duration, at time.Time) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	row := dbToken{
		TokenHash: hash,
		Purpose:   purpose,
		Callsign:  normalizeCallsign(callsign),
		Email:     sanitizeSingleLineText(email),
		ChangeID:  changeID,
		CreatedAt: at.Unix(),
		ExpiresAt: at.Add(ttl).Unix(),
	}
	if err := a.db.Create(&row).Error; err != nil {
		return "", err
	}
	return token, nil
}

// recentTokens counts tokens of a purpose issued for callsign since `since`.
func (a *app) recentTokens(purpose, callsign string, since time.Time) (int64, error) {
	var count int64
	err := a.db.Model(&dbToken{}).
		Where("purpose = ? AND callsign = ? AND created_at >= ?", purpose, normalizeCallsign(callsign), since.Unix()).
		Count(&count).Error
	return count, err
}

// emailChange is one pending double-confirmation of an email address.
type emailChange struct {
	ChangeID string
	Callsign string
	OldEmail string
	NewEmail string
	OldDone  bool
	NewDone  bool
}

// pendingEmailChanges groups unconsumed or half-confirmed change tokens.
func (a *app) pendingEmailChanges() ([]emailChange, error) {
	rows := []dbToken{}
	err := a.db.Where("purpose IN ?", []string{tokenPurposeEmailOld, tokenPurposeEmailNew}).
		Order("id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	byID := map[string]*emailChange{}
	order := []string{}
	for _, row := range rows {
		if row.ChangeID == "" {
			continue
		}
		change, ok := byID[row.ChangeID]
		if !ok {
			change = &emailChange{ChangeID: row.ChangeID, Callsign: row.Callsign}
			byID[row.ChangeID] = change
			order = append(order, row.ChangeID)
		}
		switch row.Purpose {
		case tokenPurposeEmailOld:
			change.OldEmail = row.Email
			change.OldDone = row.ConsumedAt > 0
		case tokenPurposeEmailNew:
			change.NewEmail = row.Email
			change.NewDone = row.ConsumedAt > 0
		}
	}
	out := make([]emailChange, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// applyEmailChanges closes the loop on an email change.
//
// The web BBS owns the click: it marks a token consumed when the link is
// opened. When both halves of a change are consumed, the address is applied
// here, so the web front end never has to write to the user table itself.
func (a *app) applyEmailChanges(at time.Time) error {
	changes, err := a.pendingEmailChanges()
	if err != nil {
		return err
	}
	for _, change := range changes {
		if !change.OldDone || !change.NewDone {
			// Expired halves are cleared so they cannot be completed later.
			if err := a.expireEmailChange(change.ChangeID, at); err != nil {
				return err
			}
			continue
		}
		profile, ok, err := a.loadUser(change.Callsign)
		if err != nil {
			return err
		}
		if !ok {
			// The account is gone; drop the tokens rather than retrying forever.
			if err := a.deleteEmailChangeTokens(change.ChangeID); err != nil {
				return err
			}
			continue
		}
		previousEmail := profile.Email
		profile.Email = sanitizeSingleLineText(change.NewEmail)
		if err := a.saveUser(change.Callsign, profile); err != nil {
			return err
		}
		if err := a.deleteEmailChangeTokens(change.ChangeID); err != nil {
			return err
		}
		a.logBBSAction(change.Callsign, "email_change_applied", "from=%q to=%q", previousEmail, change.NewEmail)
	}
	return nil
}

// expireEmailChange drops both tokens of a change that can no longer complete.
func (a *app) expireEmailChange(changeID string, at time.Time) error {
	rows := []dbToken{}
	err := a.db.Where("change_id = ?", changeID).Find(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ConsumedAt > 0 {
			continue
		}
		if at.Unix() <= row.ExpiresAt {
			return nil
		}
	}
	return a.deleteEmailChangeTokens(changeID)
}

func (a *app) deleteEmailChangeTokens(changeID string) error {
	return a.db.Where("change_id = ?", changeID).Delete(&dbToken{}).Error
}

// pruneTokens drops spent and expired rows past their retention, and activation
// codes whose expiry has passed.
func (a *app) pruneTokens(at time.Time) error {
	if err := a.db.Where("expires_at < ?", at.Add(-tokenRetention).Unix()).Delete(&dbToken{}).Error; err != nil {
		return err
	}
	return a.db.Where("expires_at < ?", at.Unix()).Delete(&dbActivation{}).Error
}
