package main

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Row-level persistence (H2).
//
// The original code mutated data by rewriting whole tables: loadBoards() read
// every board and message, the caller appended to that in-memory copy, and
// saveBoards() ran DELETE FROM db_messages / DELETE FROM db_boards and re-inserted
// the copy. Each SSH session is a separate bbs_app process, so two sessions that
// started from the same snapshot silently deleted each other's rows. saveUsers()
// was worse: it deleted every row its snapshot did not contain, which killed
// accounts registered concurrently by another session.
//
// Every function here touches only the rows it names, inside one short
// transaction, so concurrent writers contend for SQLite's write lock instead of
// overwriting each other. openDatabase() opens with _txlock=immediate, so a
// read-then-write transaction takes the write lock at BEGIN rather than at its
// first write; combined with _busy_timeout it makes concurrent writers wait
// their turn instead of interleaving.
//
// The *Row functions take a *gorm.DB so seeding can run them inside one
// transaction; the app methods are the request-path entry points.

// saveUser inserts or updates exactly one account.
func (a *app) saveUser(callsign string, profile userProfile) error {
	return saveUserRow(a.db, callsign, profile)
}

func saveUserRow(db *gorm.DB, callsign string, profile userProfile) error {
	callsign = normalizeCallsign(callsign)
	if callsign == "" {
		return fmt.Errorf("saveUser: empty callsign")
	}
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
		Registered:   profile.Registered,
		Activated:    profile.Activated,
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "callsign"}},
		UpdateAll: true,
	}).Create(&row).Error
}

// deleteUser removes one account and its APRS history in a single transaction.
func (a *app) deleteUser(callsign string) error {
	key := normalizeCallsign(callsign)
	if key == "" {
		return nil
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := deleteAPRSHistoryRow(tx, key); err != nil {
			return err
		}
		return tx.Delete(&dbUser{}, "callsign = ?", key).Error
	})
}

// createBoard appends one board at the end of the list.
func (a *app) createBoard(b board) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		return createBoardRow(tx, b)
	})
}

func createBoardRow(db *gorm.DB, b board) error {
	id := boardID(firstNonEmpty(b.ID, b.Name).(string))
	if id == "" {
		id = defaultBoardID
	}
	name := clip(sanitizeSingleLineText(firstNonEmpty(b.Name, id).(string)), 60)
	description := clip(sanitizeSingleLineText(b.Description), 120)
	created := firstNonEmpty(b.Created, now()).(string)
	var maxPos int
	if err := db.Model(&dbBoard{}).Select("COALESCE(MAX(position), -1)").Scan(&maxPos).Error; err != nil {
		return err
	}
	return db.Create(&dbBoard{ID: id, Position: maxPos + 1, Name: name, Description: description, Created: created}).Error
}

// renameBoardRow updates one board's name and, when the derived ID changes,
// moves its messages to the new ID in the same transaction. A half-applied
// rename would orphan every message on the board.
func (a *app) renameBoardRow(oldID, newID, name string) error {
	oldKey := boardID(oldID)
	newKey := boardID(newID)
	if oldKey == "" || newKey == "" {
		return fmt.Errorf("renameBoard: empty board id")
	}
	name = clip(sanitizeSingleLineText(name), 60)
	return a.db.Transaction(func(tx *gorm.DB) error {
		if oldKey != newKey {
			if err := tx.Model(&dbMessage{}).Where("board_id = ?", oldKey).Update("board_id", newKey).Error; err != nil {
				return err
			}
			if err := tx.Model(&dbBoard{}).Where("id = ?", oldKey).Update("id", newKey).Error; err != nil {
				return err
			}
		}
		return tx.Model(&dbBoard{}).Where("id = ?", newKey).Update("name", name).Error
	})
}

// deleteBoardRow removes one board and its messages.
func (a *app) deleteBoardRow(id string) error {
	key := boardID(id)
	if key == "" {
		return nil
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&dbMessage{}, "board_id = ?", key).Error; err != nil {
			return err
		}
		return tx.Delete(&dbBoard{}, "id = ?", key).Error
	})
}

// createMessage appends one message or reply and returns its row ID, which the
// UI needs to address the row later without re-reading the whole board.
func (a *app) createMessage(boardKey string, parentID *uint, msg message) (uint, error) {
	var id uint
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = createMessageRow(tx, boardKey, parentID, msg)
		return err
	})
	return id, err
}

var errThreadTooDeep = fmt.Errorf("thread nesting exceeds %d levels", maxReplyDepth)

// messageDepth returns how many levels below the top level id sits: 0 for a
// top-level message, 1 for its reply, and so on. A corrupt parent chain
// terminates instead of looping forever.
func messageDepth(db *gorm.DB, id uint) (int, error) {
	depth := 0
	current := id
	for {
		if depth > maxReplyDepth {
			return depth, errThreadTooDeep
		}
		row := dbMessage{}
		err := db.Select("id", "parent_id").Where("id = ?", current).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return depth, nil
		}
		if err != nil {
			return depth, err
		}
		if row.ParentID == nil {
			return depth, nil
		}
		current = *row.ParentID
		depth++
	}
}

func createMessageRow(db *gorm.DB, boardKey string, parentID *uint, msg message) (uint, error) {
	if parentID != nil {
		parentDepth, err := messageDepth(db, *parentID)
		if err != nil {
			return 0, err
		}
		if parentDepth+1 > maxReplyDepth {
			return 0, errThreadTooDeep
		}
	}
	// A message on a board with no row would be invisible: loadBoards() reads
	// boards from the table, so the orphan would never be rendered. The UI
	// renders a virtual "General" board when the table is empty, so materialise
	// it on first write rather than dropping the post.
	if err := ensureBoardExists(db, boardKey); err != nil {
		return 0, err
	}
	row := dbMessage{
		BoardID:  boardKey,
		ParentID: parentID,
		From:     sanitizeSingleLineText(msg.From),
		Subject:  sanitizeSingleLineText(msg.Subject),
		Body:     sanitizeMultilineText(msg.Body),
		Created:  msg.Created,
		Edited:   sanitizeSingleLineText(msg.Edited),
	}
	if row.Created == "" {
		row.Created = now()
	}
	siblings := db.Model(&dbMessage{}).Where("board_id = ?", boardKey)
	if parentID == nil {
		siblings = siblings.Where("parent_id IS NULL")
	} else {
		siblings = siblings.Where("parent_id = ?", *parentID)
	}
	var maxPos int
	if err := siblings.Select("COALESCE(MAX(position), -1)").Scan(&maxPos).Error; err != nil {
		return 0, err
	}
	row.Position = maxPos + 1
	if err := db.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func ensureBoardExists(db *gorm.DB, boardKey string) error {
	var count int64
	if err := db.Model(&dbBoard{}).Where("id = ?", boardKey).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if boardKey == defaultBoardID {
		return createBoardRow(db, defaultBoard(nil))
	}
	return createBoardRow(db, board{ID: boardKey})
}

// updateMessageRow rewrites one message's editable fields.
func (a *app) updateMessageRow(id uint, subject, body, edited string) error {
	if id == 0 {
		return fmt.Errorf("updateMessage: missing message id")
	}
	return a.db.Model(&dbMessage{}).Where("id = ?", id).Updates(map[string]any{
		"subject": sanitizeSingleLineText(subject),
		"body":    sanitizeMultilineText(body),
		"edited":  sanitizeSingleLineText(edited),
	}).Error
}

// deleteMessageSubtree removes a message and every reply beneath it. Deleting
// only the root would leave orphaned replies that no longer appear anywhere in
// the tree view.
func (a *app) deleteMessageSubtree(boardKey string, id uint) error {
	if id == 0 {
		return nil
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		var rows []dbMessage
		if err := tx.Select("id", "parent_id").Where("board_id = ?", boardKey).Find(&rows).Error; err != nil {
			return err
		}
		ids := messageSubtreeIDs(rows, id)
		if len(ids) == 0 {
			return nil
		}
		return tx.Delete(&dbMessage{}, "id IN ?", ids).Error
	})
}

// messageSubtreeIDs returns root and its descendants. A visited set makes a
// corrupt parent chain terminate instead of recursing forever.
func messageSubtreeIDs(rows []dbMessage, root uint) []uint {
	children := map[uint][]uint{}
	for _, row := range rows {
		if row.ParentID != nil {
			children[*row.ParentID] = append(children[*row.ParentID], row.ID)
		}
	}
	var out []uint
	seen := map[uint]bool{}
	var walk func(uint)
	walk = func(id uint) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		for _, child := range children[id] {
			walk(child)
		}
	}
	walk(root)
	return out
}

// trimBoardMessages enforces the per-board top-level message cap by deleting the
// oldest threads, matching the previous in-memory trim but without rewriting
// rows that did not change.
func (a *app) trimBoardMessages(boardKey string, limit int) error {
	if limit <= 0 {
		return nil
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		var rows []dbMessage
		if err := tx.Select("id", "parent_id", "position").Where("board_id = ? AND parent_id IS NULL", boardKey).Order("position, id").Find(&rows).Error; err != nil {
			return err
		}
		excess := len(rows) - limit
		if excess <= 0 {
			return nil
		}
		var all []dbMessage
		if err := tx.Select("id", "parent_id").Where("board_id = ?", boardKey).Find(&all).Error; err != nil {
			return err
		}
		var doomed []uint
		for _, row := range rows[:excess] {
			doomed = append(doomed, messageSubtreeIDs(all, row.ID)...)
		}
		if len(doomed) == 0 {
			return nil
		}
		return tx.Delete(&dbMessage{}, "id IN ?", doomed).Error
	})
}

// createBulletin appends one bulletin and returns its row ID.
func (a *app) createBulletin(b bulletin) (uint, error) {
	var id uint
	err := a.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = createBulletinRow(tx, b)
		return err
	})
	return id, err
}

func createBulletinRow(db *gorm.DB, b bulletin) (uint, error) {
	row := dbBulletin{
		Title:   sanitizeSingleLineText(b.Title),
		Body:    sanitizeMultilineText(b.Body),
		Updated: b.Updated,
		From:    sanitizeSingleLineText(b.From),
	}
	if row.Updated == "" {
		row.Updated = now()
	}
	var maxPos int
	if err := db.Model(&dbBulletin{}).Select("COALESCE(MAX(position), -1)").Scan(&maxPos).Error; err != nil {
		return 0, err
	}
	row.Position = maxPos + 1
	if err := db.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// updateBulletinRow rewrites one bulletin's editable fields.
func (a *app) updateBulletinRow(id uint, title, body, updated, from string) error {
	if id == 0 {
		return fmt.Errorf("updateBulletin: missing bulletin id")
	}
	return a.db.Model(&dbBulletin{}).Where("id = ?", id).Updates(map[string]any{
		"title":   sanitizeSingleLineText(title),
		"body":    sanitizeMultilineText(body),
		"updated": updated,
		"from":    sanitizeSingleLineText(from),
	}).Error
}

// deleteBulletinRow removes one bulletin.
func (a *app) deleteBulletinRow(id uint) error {
	if id == 0 {
		return nil
	}
	return a.db.Delete(&dbBulletin{}, id).Error
}
