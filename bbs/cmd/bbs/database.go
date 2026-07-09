package main

import (
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
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&dbUser{}, &dbBulletin{}, &dbBoard{}, &dbMessage{}, &dbAPRSSent{}, &dbAPRSSentPart{}, &dbAPRSReceived{}); err != nil {
		return nil, err
	}
	return db, nil
}

func (a *app) seedDefaultData() error {
	var count int64
	if err := a.db.Model(&dbBulletin{}).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		bulletins := []bulletin{
			{Title: "Welcome", Body: "This is a small HamNet-ready BBS for radio operators.\nUse it for local notes, net announcements, and station contact info.", Updated: now()},
			{Title: "Operating Notes", Body: "Keep traffic courteous and relevant to amateur radio.\nDo not post private keys, passwords, or third-party personal data.", Updated: now()},
		}
		if err := a.saveBulletins(bulletins); err != nil {
			return err
		}
	}
	if err := a.db.Model(&dbBoard{}).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return a.saveBoards(boardsData{Boards: []board{defaultBoard(nil)}})
	}
	return nil
}
