package zwibserve

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" // include sqlite3
)

const schema = `
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid TEXT PRIMARY KEY, 
	lastAccess INTEGER,
	data BLOB
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	docID TEXT,
	name TEXT,
	value TEXT,
	version NUMBER,
	UNIQUE(docID, name),
	FOREIGN KEY (docID) REFERENCES ZwibblerDocs(docID) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ZwibblerTokens (
	tokenID TEXT UNIQUE,
	docID TEXT,
	userID TEXT,
	permissions TEXT,
	expiration INTEGER
);

CREATE INDEX IF NOT EXISTS ZwibblerTokenUserIndex ON ZwibblerTokens(userID);
`

// SQLITEDocumentDB is a document database using SQLITE. The documents are all stored in a single file database.
type SQLITEDocumentDB struct {
	lastClean  time.Time
	conn       *sqlx.DB
	expiration int64
}

// NewSQLITEDB creates a new document storage based on SQLITE
func NewSQLITEDB(filename string) DocumentDB {

	sqldb, err := sqlx.Connect("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=5000&mode=rwc&_journal_mode=WAL&cache=shared", filename))

	if err != nil {
		log.Panic(err)
	}

	sqldb.SetMaxOpenConns(1)

	sqldb.MustExec(schema)

	db := &SQLITEDocumentDB{
		conn: sqldb,
	}

	db.clean()
	return db
}

// SetExpiration ...
func (db *SQLITEDocumentDB) SetExpiration(seconds int64) {
	db.expiration = seconds
}

func (db *SQLITEDocumentDB) clean() {
	seconds := db.expiration
	if seconds == 0 {
		seconds = 24 * 60 * 60
	} else if seconds == NoExpiration {
		return
	}

	now := time.Now()
	if time.Since(db.lastClean).Minutes() < 60 {
		return
	}

	db.conn.MustExec("DELETE FROM ZwibblerDocs WHERE lastAccess < ?",
		now.Unix()-seconds)
	db.conn.MustExec("DELETE FROM ZwibblerTokens WHERE expiration < ?",
		now.Unix())
	db.lastClean = now
}

// GetDocument ...
func (db *SQLITEDocumentDB) GetDocument(docID string, mode CreateMode, initialData []byte) ([]byte, bool, error) {
	db.clean()

	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query("SELECT data FROM ZwibblerDocs WHERE docid=?", docID)
	if err != nil {
		log.Panic(err)
	}
	defer rows.Close()

	created := true
	var doc []byte
	if rows.Next() {
		err = rows.Scan(&doc)
		if err != nil {
			log.Panic(err)
		}
		created = false
	}

	if doc == nil && mode == NeverCreate {
		return nil, false, ErrMissing
	} else if doc != nil && mode == AlwaysCreate {
		return nil, false, ErrExists
	}

	if doc == nil {
		doc = initialData
		tx.MustExec(`INSERT INTO ZwibblerDocs (docid, lastAccess, data) VALUES (?, ?, ?)`,
			docID, time.Now().Unix(), doc)
	} else {
		tx.MustExec("UPDATE ZwibblerDocs set lastAccess=? WHERE docid=?", time.Now().Unix(), docID)
	}

	return doc, created, nil

}

// AppendDocument ...
func (db *SQLITEDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	db.clean()
	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query("SELECT data FROM ZwibblerDocs WHERE docid=?", docID)
	if err != nil {
		log.Panic(err)
	}
	defer rows.Close()

	var doc []byte
	if rows.Next() {
		err = rows.Scan(&doc)
		if err != nil {
			log.Panic(err)
		}
	}

	if doc == nil {
		return 0, ErrMissing
	}

	if uint64(len(doc)) != oldLength {
		return uint64(len(doc)), ErrConflict
	}

	doc = append(doc, newData...)
	tx.MustExec("UPDATE ZwibblerDocs SET data=? WHERE docid=?", doc, docID)
	tx.MustExec("UPDATE ZwibblerDocs set lastAccess=? WHERE docid=?", time.Now().Unix(), docID)

	return uint64(len(doc)), nil
}

// GetDocumentKeys ...
func (db *SQLITEDocumentDB) GetDocumentKeys(docID string) ([]Key, error) {
	var keys []Key

	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query("SELECT name, value, version FROM ZwibblerKeys WHERE docid=?", docID)
	if err != nil {
		log.Panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var key Key
		err = rows.Scan(&key.Name, &key.Value, &key.Version)
		if err != nil {
			log.Panic(err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// SetDocumentKey ...
func (db *SQLITEDocumentDB) SetDocumentKey(docID string, oldVersion int, key Key) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()

	var dbVersion int
	exists := false

	rows, err := tx.Query("SELECT version FROM ZwibblerKeys WHERE docid=? AND name=?", docID, key.Name)
	if err != nil {
		log.Panic(err)
	}
	defer rows.Close()
	if rows.Next() {
		exists = true
		err = rows.Scan(&dbVersion)
		if err != nil {
			log.Panic(err)
		}
	}
	rows.Close()

	// if the key exists, and the old version does not match, then fail.
	if exists && dbVersion != oldVersion {
		return ErrConflict
	} else if !exists && oldVersion != 0 {
		return ErrConflict
	}

	// if the key exists, perform update. otherwise, perform insert.
	if exists {
		tx.MustExec(`UPDATE ZwibblerKeys SET value=?, version=? WHERE docID=? AND name=?`, key.Value, key.Version, docID, key.Name)
	} else {
		tx.MustExec(`INSERT INTO ZwibblerKeys(docID, name, value, version) VALUES (?, ?, ?, ?)`,
			docID, key.Name, key.Value, key.Version)
	}

	return nil
}

func (db *SQLITEDocumentDB) DeleteDocument(docID string) error {
	db.conn.MustExec("DELETE FROM ZwibblerDocs WHERE docID=?", docID)
	return nil
}

// AddToken ...
func (db *SQLITEDocumentDB) AddToken(tokenID, docID, userID, permissions string, expirationSeconds int64, contents []byte) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()

	// check if doc exists
	if len(contents) > 0 {
		rows, err := tx.Query("SELECT data FROM ZwibblerDocs WHERE docid=?", docID)
		if err != nil {
			log.Panic(err)
		}
		defer rows.Close()
		if rows.Next() {
			return ErrConflict
		}

		tx.MustExec(`INSERT INTO ZwibblerDocs (docid, lastAccess, data) VALUES (?, ?, ?)`,
			docID, time.Now().Unix(), contents)
	}

	_, err := tx.Exec("INSERT INTO ZwibblerTokens(tokenID, docID, userID, permissions, expiration) VALUES (?, ?, ?, ?, ?)",
		tokenID, docID, userID, permissions, expirationSeconds)

	if err != nil {
		tx.Rollback()
		if strings.Contains(err.Error(), "UNIQUE") {
			err = ErrExists
		}

	}

	return err
}

func (db *SQLITEDocumentDB) GetToken(token string) (docID, userID, permissions string, err error) {
	tx := db.conn.MustBegin()
	defer tx.Commit()
	now := time.Now().Unix()
	rows, err := tx.Query("SELECT docID, userID, permissions FROM ZwibblerTokens WHERE tokenID=? AND expiration > ?", token, now)
	if err != nil {
		return
	}
	defer rows.Close()

	if rows.Next() {
		err = rows.Scan(&docID, &userID, &permissions)
	} else {
		err = ErrMissing
	}

	return
}

func (db *SQLITEDocumentDB) UpdateUser(userID, permissions string) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()
	tx.MustExec("UPDATE ZwibblerTokens SET permissions=? WHERE userID=?", userID, permissions)
	return nil
}
