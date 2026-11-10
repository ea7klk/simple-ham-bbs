package main

import (
	"gorm.io/gorm"
)

// Bulk-replace helpers for test fixtures.
//
// They live in the test binary on purpose: H2 was caused by rewriting whole
// tables from an in-memory snapshot, and keeping that primitive out of the
// shipped binary is the mechanism that stops a future caller from reaching for
// it on a request path. Production code mutates single rows through
// mutations.go.

func (a *app) replaceAllUsers(users map[string]userProfile) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&dbUser{}).Error; err != nil {
			return err
		}
		for key, profile := range users {
			if normalizeCallsign(key) == "" {
				continue
			}
			if err := saveUserRow(tx, key, profile); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *app) replaceAllBulletins(items []bulletin) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&dbBulletin{}).Error; err != nil {
			return err
		}
		for _, item := range items {
			if _, err := createBulletinRow(tx, item); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *app) replaceAllBoards(data boardsData) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&dbMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&dbBoard{}).Error; err != nil {
			return err
		}
		for _, b := range data.Boards {
			if err := createBoardRow(tx, b); err != nil {
				return err
			}
			id := boardID(firstNonEmpty(b.ID, b.Name).(string))
			if id == "" {
				id = defaultBoardID
			}
			if err := fixtureMessageRows(tx, id, nil, b.Messages); err != nil {
				return err
			}
		}
		return nil
	})
}

func fixtureMessageRows(tx *gorm.DB, boardKey string, parentID *uint, messages []message) error {
	for _, msg := range messages {
		id, err := createMessageRow(tx, boardKey, parentID, msg)
		if err != nil {
			return err
		}
		child := id
		if err := fixtureMessageRows(tx, boardKey, &child, msg.Replies); err != nil {
			return err
		}
	}
	return nil
}
