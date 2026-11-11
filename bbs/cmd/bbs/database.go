package main

import (
	"errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type dbUser struct {
	Callsign     string `gorm:"primaryKey;size:32"`
	FullName     string
	Email        string
	Maidenhead   string
	Language     string
	EnableAPRS   bool
	QTH          string
	Rig          string
	PasswordHash string
	IsSysop      bool
	Disabled     bool
	FirstSeen    string
	LastSeen     string
	Registered   string
	Activated    bool
}

type dbBulletin struct {
	ID       uint `gorm:"primaryKey"`
	Position int  `gorm:"index"`
	Title    string
	Body     string
	Updated  string
	From     string
}

type dbBoard struct {
	ID          string `gorm:"primaryKey;size:64"`
	Position    int    `gorm:"index"`
	Name        string
	Description string
	Created     string
}

type dbMessage struct {
	ID       uint   `gorm:"primaryKey"`
	BoardID  string `gorm:"index"`
	ParentID *uint  `gorm:"index"`
	Position int    `gorm:"index"`
	From     string
	Subject  string
	Body     string
	Created  string
	Edited   string
}

type dbAPRSSent struct {
	ID           uint   `gorm:"primaryKey"`
	UserCallsign string `gorm:"index"`
	Position     int    `gorm:"index"`
	At           string
	From         string
	To           string
	Text         string
	Status       string
	Acked        bool
	Passcode     int
	Parts        []dbAPRSSentPart `gorm:"foreignKey:SentID;constraint:OnDelete:CASCADE"`
}

type dbAPRSSentPart struct {
	ID        uint `gorm:"primaryKey"`
	SentID    uint `gorm:"index"`
	Number    int
	Text      string
	Status    string
	Detail    string
	MessageID string `gorm:"index"`
	Acked     bool
}

type dbAPRSReceived struct {
	ID           uint   `gorm:"primaryKey"`
	UserCallsign string `gorm:"index"`
	Position     int    `gorm:"index"`
	At           string
	From         string
	To           string
	Text         string
	Raw          string
}

func openDatabase(path string) (*gorm.DB, error) {
	// _txlock=immediate makes every transaction take the write lock at BEGIN
	// instead of at its first write. Each SSH session is its own process, so
	// this is what serialises concurrent read-then-write transactions instead
	// of letting two of them interleave and lose an update. _busy_timeout makes
	// the waiting writer block rather than fail.
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL&_txlock=immediate"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&dbUser{}, &dbBulletin{}, &dbBoard{}, &dbMessage{}, &dbAPRSSent{}, &dbAPRSSentPart{}, &dbAPRSReceived{}, &dbAuthFailure{}, &dbRegistration{}, &dbActivation{}, &dbToken{}, &dbSetting{}); err != nil {
		return nil, err
	}
	if err := backfillRegistered(db); err != nil {
		return nil, err
	}
	if err := backfillActivated(db); err != nil {
		return nil, err
	}
	return db, nil
}

// backfillActivated marks pre-existing accounts as activated.
//
// The column defaults to false, which is right for a new registration and wrong
// for every account that existed before activation was introduced, so two things
// have to happen:
//
//   - Rows where the column is NULL were created by the migration that added it.
//     NULL is never a deliberate value, so those are corrected on every start.
//     This also repairs a database where an earlier version of this function ran
//     with a `= false` predicate that could not match NULL and then recorded the
//     one-time marker anyway.
//   - Rows that are explicitly false predate activation too, but only the first
//     time: a later account that is deliberately unactivated must stay that way.
//     The marker row bounds it to one sweep.
func backfillActivated(db *gorm.DB) error {
	const key = "migrated.activation"
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&dbUser{}).Where("activated IS NULL").UpdateColumn("activated", true).Error; err != nil {
			return err
		}
		marker := dbSetting{}
		err := tx.Where("key = ?", key).Take(&marker).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Model(&dbUser{}).Where("activated = ?", false).UpdateColumn("activated", true).Error; err != nil {
			return err
		}
		return tx.Create(&dbSetting{Key: key, Value: "1"}).Error
	})
}

// backfillRegistered gives accounts created before the column existed a
// registration date taken from their first-seen stamp, which was written at
// creation. It is idempotent: after the first run the WHERE clause matches
// nothing.
func backfillRegistered(db *gorm.DB) error {
	return db.Model(&dbUser{}).
		Where("(registered IS NULL OR registered = '') AND first_seen IS NOT NULL AND first_seen <> ''").
		UpdateColumn("registered", gorm.Expr("first_seen")).Error
}

// seedDefaultData inserts the starter bulletins and board. Every SSH connection
// runs this, so the count check and the inserts share one immediate transaction:
// two simultaneous logins serialise, and the second sees the first one's rows
// instead of inserting a duplicate set.
func (a *app) seedDefaultData() error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&dbBulletin{}).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			for _, b := range []bulletin{
				{Title: "Welcome", Body: "This is a small HamNet-ready BBS for radio operators.\nUse it for local notes, net announcements, and station contact info.", Updated: now()},
				{Title: "Operating Notes", Body: "Keep traffic courteous and relevant to amateur radio.\nDo not post private keys, passwords, or third-party personal data.", Updated: now()},
			} {
				if _, err := createBulletinRow(tx, b); err != nil {
					return err
				}
			}
		}
		if err := tx.Model(&dbBoard{}).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return createBoardRow(tx, defaultBoard(nil))
		}
		return nil
	})
}
