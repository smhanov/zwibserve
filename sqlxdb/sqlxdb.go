package sqlxdb

import (
	"database/sql"
	"github.com/smhanov/zwibserve"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
)

func now() int64 {
	return time.Now().Unix()
}

// SQLXDocumentDB is a document database using SQLX. The documents are all stored in a single file database.
type SQLXDocumentDB struct {
	lastClean  time.Time
	conn       *sqlx.DB
	expiration int64
	autoClean  bool
}

// NewSQLXDB creates a new document storage based on SQLX
func NewSQLXDB(driverName, dataSourceName string, schema []string) zwibserve.DocumentDB {

	sqldb, err := sqlx.Connect(driverName, dataSourceName)

	if err != nil {
		log.Panic(err)
	}

	sqldb.SetMaxOpenConns(1)

	for i := 0; i < len(schema); i++ {
		sqldb.MustExec(schema[i])
	}

	db := &SQLXDocumentDB{
		conn:      sqldb,
		autoClean: false,
	}

	db.clean()
	return db
}

// SetExpiration ...
func (db *SQLXDocumentDB) SetExpiration(seconds int64) {
	db.expiration = seconds
}

func (db *SQLXDocumentDB) tx() *sqlx.Tx {
	return db.conn.MustBegin()
}

func (db *SQLXDocumentDB) query(tx *sqlx.Tx, query string, args ...interface{}) *sql.Rows {
	rows, err := tx.Query(db.conn.Rebind(query), args...)
	if err != nil {
		log.Panic(err)
	}
	return rows
}

func (db *SQLXDocumentDB) exec(tx *sqlx.Tx, query string, args ...interface{}) {
	tx.MustExec(db.conn.Rebind(query), args...)
}

func (db *SQLXDocumentDB) commit(tx *sqlx.Tx) {
	err := tx.Commit()
	if err != nil {
		log.Fatal(err)
	}
}

func (db *SQLXDocumentDB) close(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func (db *SQLXDocumentDB) scan(rows *sql.Rows, fields ...interface{}) {
	err := rows.Scan(fields...)
	if err != nil {
		log.Panic(err)
	}

}

func (db *SQLXDocumentDB) clean() {
	if db.autoClean {
		seconds := db.expiration
		if seconds == 0 {
			seconds = 24 * 60 * 60
		} else if seconds == zwibserve.NoExpiration {
			return
		}

		now := time.Now()
		if time.Since(db.lastClean).Minutes() < 60 {
			return
		}

		tx := db.tx()
		defer db.commit(tx)

		db.exec(tx, "DELETE FROM ZwibblerDocs WHERE lastAccess < ?",
			now.Unix()-seconds)
		db.lastClean = now
	}
}

// GetDocument ...
func (db *SQLXDocumentDB) GetDocument(docID string, mode zwibserve.CreateMode, initialData []byte) ([]byte, bool, error) {
	db.clean()

	tx := db.tx()
	defer db.commit(tx)

	rows := db.query(tx, "SELECT data FROM ZwibblerDocs WHERE docid = ?", docID)
	created := true
	var doc []byte
	if rows.Next() {
		db.scan(rows, &doc)
		created = false
	}
	db.close(rows)

	if doc == nil && mode == zwibserve.NeverCreate {
		return nil, false, zwibserve.ErrMissing
	} else if doc != nil && mode == zwibserve.AlwaysCreate {
		return nil, false, zwibserve.ErrExists
	}

	if doc == nil {
		doc = initialData
		db.exec(tx, `INSERT INTO ZwibblerDocs (docid, lastAccess, data) VALUES (?, ?, ?)`,
			docID, now(), doc)
	} else {
		db.exec(tx, "UPDATE ZwibblerDocs set lastAccess = ? WHERE docid = ?", now(), docID)
	}

	return doc, created, nil

}

// AppendDocument ...
func (db *SQLXDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	db.clean()
	tx := db.tx()
	defer db.commit(tx)

	rows := db.query(tx, "SELECT data FROM ZwibblerDocs WHERE docid = ?", docID)

	var doc []byte
	if rows.Next() {
		db.scan(rows, &doc)
	}
	db.close(rows)

	if doc == nil {
		return 0, zwibserve.ErrMissing
	}

	if uint64(len(doc)) != oldLength {
		return uint64(len(doc)), zwibserve.ErrConflict
	}

	doc = append(doc, newData...)
	db.exec(tx, "UPDATE ZwibblerDocs SET data = ? WHERE docid = ?", doc, docID)
	db.exec(tx, "UPDATE ZwibblerDocs set lastAccess = ? WHERE docid = ?", now(), docID)

	return uint64(len(doc)), nil
}

// GetDocumentKeys ...
func (db *SQLXDocumentDB) GetDocumentKeys(docID string) ([]zwibserve.Key, error) {
	var keys []zwibserve.Key

	tx := db.tx()
	defer db.commit(tx)

	rows := db.query(tx, "SELECT name, value, version FROM ZwibblerKeys WHERE docid = ?", docID)

	for rows.Next() {
		var key zwibserve.Key
		db.scan(rows, &key.Name, &key.Value, &key.Version)
		keys = append(keys, key)
	}
	db.close(rows)

	return keys, nil
}

// SetDocumentKey ...
func (db *SQLXDocumentDB) SetDocumentKey(docID string, oldVersion int, key zwibserve.Key) error {
	tx := db.tx()
	defer db.commit(tx)

	var dbVersion int
	exists := false

	rows := db.query(tx, "SELECT version FROM ZwibblerKeys WHERE docid = ? AND name = ?", docID, key.Name)
	if rows.Next() {
		exists = true
		db.scan(rows, &dbVersion)
	}
	db.close(rows)

	// if the key exists, and the old version does not match, then fail.
	if exists && dbVersion != oldVersion {
		return zwibserve.ErrConflict
	} else if !exists && oldVersion != 0 {
		return zwibserve.ErrConflict
	}

	// if the key exists, perform update. otherwise, perform insert.
	if exists {
		db.exec(tx, `UPDATE ZwibblerKeys SET value = ?, version = ? WHERE docid = ? AND name = ?`,
			key.Value, key.Version, docID, key.Name)
	} else {
		db.exec(tx, `INSERT INTO ZwibblerKeys(docid, name, value, version) VALUES (?, ?, ?, ?)`,
			docID, key.Name, key.Value, key.Version)
	}

	return nil
}
