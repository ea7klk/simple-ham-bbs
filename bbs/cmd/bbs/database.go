package main

import (
	"errors"
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type dbUser struct {
	Callsign string `gorm:"primaryKey;size:32"`
	FullName string
	// Email is indexed with COLLATE NOCASE. SQLite only uses that index for a
	// query that names the collation, so every lookup by address must say
	// COLLATE NOCASE too — a bare `email = ?` scans the table.
	Email        string `gorm:"index:idx_users_email_nocase,collate:nocase"`
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

// dbAPRSBulletin is one APRS bulletin, shared by all users rather than stored
// per recipient: a bulletin addresses BLNn + a group, not a callsign.
//
// Bulletins repeat — weather products are retransmitted every few minutes — so
// the row is keyed by content (DedupeKey) and a repeat bumps SeenCount and
// LastSeen instead of adding a row.
type dbAPRSBulletin struct {
	ID        uint   `gorm:"primaryKey"`
	DedupeKey string `gorm:"uniqueIndex;size:64"`
	FirstSeen string `gorm:"index"`
	LastSeen  string `gorm:"index"`
	SeenCount int
	From      string `gorm:"index"`
	Slot      int
	Group     string `gorm:"size:8;index"`
	Category  string `gorm:"size:24;index"`
	Text      string
	Raw       string
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
	return db, nil
}

// migrateDatabase brings the schema up to date and records that it did.
//
// This used to run inside openDatabase, which meant every SSH session — its own
// process — ran AutoMigrate and two backfills before it could show a login
// prompt. AutoMigrate introspects every table, so the cost grew with the user
// count: measured at 2.5 s per login at 5 000 users and 4.6 s at 10 000
// (SCALABILITY.md). It also took the write lock on every connection.
//
// It now runs once, from the supervisor (or an explicit `bbs_app migrate`), and
// records completion so a session can tell whether the one-shot has happened
// without doing the work itself.
// schemaModels lists every table the migration is expected to create. It exists
// so a forgotten version bump cannot leave a deployment without a table: the
// initialiser checks these are actually present, not just that a marker says so.
func schemaModels() []any {
	return []any{&dbUser{}, &dbBulletin{}, &dbBoard{}, &dbMessage{}, &dbAPRSSent{},
		&dbAPRSSentPart{}, &dbAPRSReceived{}, &dbAPRSBulletin{}, &dbAuthFailure{},
		&dbRegistration{}, &dbActivation{}, &dbToken{}, &dbSetting{}}
}

// missingTables reports which of the expected tables do not exist.
//
// A marker in db_settings says a migration ran, but it cannot say the migration
// ran *this* code. Adding a table without bumping schemaVersion is an easy
// mistake — it shipped once — and the symptom is a runtime "no such table"
// rather than a failed startup. Checking presence turns that into a migration on
// the next start, so a forgotten bump degrades to a slower boot rather than a
// broken feature.
func missingTables(db *gorm.DB) []string {
	missing := []string{}
	for _, model := range schemaModels() {
		if db.Migrator().HasTable(model) {
			continue
		}
		name := fmt.Sprintf("%T", model)
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err == nil && stmt.Schema != nil {
			name = stmt.Schema.Table
		}
		missing = append(missing, name)
	}
	return missing
}

func migrateDatabase(db *gorm.DB) error {
	if err := db.AutoMigrate(schemaModels()...); err != nil {
		return err
	}
	if err := backfillRegistered(db); err != nil {
		return err
	}
	if err := backfillActivated(db); err != nil {
		return err
	}
	// The email index is only used by a query that names the collation, so the
	// lookup in lookupAccount must say COLLATE NOCASE too; see dbUser.Email.
	if err := trimStoredEmails(db); err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		UpdateAll: true,
	}).Create(&dbSetting{Key: schemaVersionKey, Value: schemaVersion}).Error
}

const (
	// schemaVersionKey is the marker a session reads to know the one-shot
	// migration has completed. It lives in db_settings, alongside the other
	// migration markers.
	schemaVersionKey = "schema.version"
	// schemaVersion is bumped when a migration needs to run again after a code
	// change; a session refuses to start against anything older.
	//
	// History: 1 = initial marker, 2 = db_aprs_bulletins.
	schemaVersion = "2"
)

// schemaReady reports whether migrateDatabase has completed against this file.
//
// A missing table and a missing row are both normal answers here — a fresh
// database has neither — so the query is not logged. The caller treats any error
// as "not ready".
func schemaReady(db *gorm.DB) bool {
	var value string
	err := db.Session(&gorm.Session{Logger: logger.Discard}).
		Raw("SELECT value FROM db_settings WHERE key = ?", schemaVersionKey).
		Scan(&value).Error
	return err == nil && value == schemaVersion
}

// trimStoredEmails removes surrounding whitespace from stored addresses.
//
// The email lookup is now an indexed equality rather than a scan that trimmed
// each row as it compared, so a legacy address written with a trailing space
// would stop matching. Trimming once, here, keeps the index authoritative. It is
// idempotent: after the first run the WHERE clause matches nothing.
func trimStoredEmails(db *gorm.DB) error {
	return db.Model(&dbUser{}).Where("email <> TRIM(email)").UpdateColumn("email", gorm.Expr("TRIM(email)")).Error
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
