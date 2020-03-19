package zwibserve

import (
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" // include sqlite3
)

const schema = `
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid TEXT PRIMARY KEY, 
	lastAccess INTEGER,
	data BLOB
);
`

// SQLITEDocumentDB is a document database using SQLITE. The documents are all stored in a single file database.
type SQLITEDocumentDB struct {
	lastClean time.Time
	conn      *sqlx.DB
}

// NewSQLITEDB creates a new document storage based on SQLITE
func NewSQLITEDB(filename string) DocumentDB {

	sqldb, err := sqlx.Connect("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=5000&mode=rwc&_journal_mode=WAL&cache=shared", filename))

	if err != nil {
		log.Panic(err)
	}

	sqldb.MustExec(schema)

	db := &SQLITEDocumentDB{
		conn: sqldb,
	}

	db.clean()
	return db
}

func (db *SQLITEDocumentDB) clean() {
	now := time.Now()
	if time.Since(db.lastClean).Minutes() < 60 {
		return
	}

	db.conn.MustExec("DELETE FROM ZwibblerDocs WHERE lastAccess < ?",
		now.Unix()-24*60*60)

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
