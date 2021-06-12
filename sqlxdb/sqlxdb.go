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
}

type Doc struct {
	id         uint64
	name       string
	autoClean  bool
	lastAccess int64
	size       int64
	seq        int64
}

type DocPart struct {
	doc  int64
	seq  int64
	data []byte
}

// NewSQLXDB creates a new document storage based on SQLX
func NewSQLXDB(driverName, dataSourceName string, schema []string, maxOpenConnections int) zwibserve.DocumentDB {

	sqldb, err := sqlx.Connect(driverName, dataSourceName)

	if err != nil {
		log.Panic(err)
	}

	sqldb.SetMaxOpenConns(maxOpenConnections)

	for i := 0; i < len(schema); i++ {
		sqldb.MustExec(schema[i])
	}

	db := &SQLXDocumentDB{
		conn: sqldb,
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

	db.exec(tx, "DELETE FROM ZwibblerDocs WHERE lastAccess < ? AND autoClean > 0",
		now.Unix()-seconds)
	db.lastClean = now
}

func (db *SQLXDocumentDB) getDocByName(tx *sqlx.Tx, name string) *Doc {
	rows := db.query(tx, "SELECT id, name, autoClean, lastAccess, size, seq FROM ZwibblerDocs WHERE name = ?", name)
	var res *Doc = nil
	if rows.Next() {
		res = &Doc{}
		db.scan(rows, &res.id, &res.name, &res.autoClean, &res.lastAccess, &res.size, &res.seq)
	}
	db.close(rows)
	return res
}

func (db *SQLXDocumentDB) appendPart(tx *sqlx.Tx, doc *Doc, data []byte) int64 {
	l := int64(len(data))
	if l > 0 {
		seq := doc.seq + 1
		size := doc.size + l
		db.exec(tx, "INSERT INTO ZwibblerDocParts (doc, seq, data) VALUES (?, ?, ?)", doc.id, seq, data)
		db.exec(tx, "UPDATE ZwibblerDocs set lastAccess = ?, size = ?, seq = ? WHERE id = ?", now(), size, seq, doc.id)
		return size
	} else {
		return doc.size
	}
}

// GetDocument ...
func (db *SQLXDocumentDB) GetDocument(docID string, mode zwibserve.CreateMode, initialData []byte) ([]byte, bool, error) {
	db.clean()

	tx := db.tx()
	defer db.commit(tx)

	doc := db.getDocByName(tx, docID)

	if doc == nil && mode == zwibserve.NeverCreate {
		return nil, false, zwibserve.ErrMissing
	} else if doc != nil && mode == zwibserve.AlwaysCreate {
		return nil, false, zwibserve.ErrExists
	} else {
		created := false
		if doc == nil {
			db.exec(tx, "INSERT INTO ZwibblerDocs (name, lastAccess, autoClean, size, seq) VALUES (?, ?, ?, ?, ?)",
				docID, now(), 1, 0, 0)
			doc = db.getDocByName(tx, docID)
			db.appendPart(tx, doc, initialData)
			created = true
		} else {
			db.exec(tx, "UPDATE ZwibblerDocs set lastAccess = ? WHERE id = ?", now(), doc.id)
		}

		rows := db.query(tx, "SELECT data FROM ZwibblerDocParts WHERE doc = ? ORDER BY seq", doc.id)
		var data []byte
		for rows.Next() {
			var rowData []byte
			db.scan(rows, &rowData)
			data = append(data, rowData...)
			created = false
		}
		db.close(rows)

		return data, created, nil
	}
}

// AppendDocument ...
func (db *SQLXDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	db.clean()
	tx := db.tx()
	defer db.commit(tx)

	doc := db.getDocByName(tx, docID)

	if doc == nil {
		return 0, zwibserve.ErrMissing
	}

	if uint64(doc.size) != oldLength {
		return uint64(doc.size), zwibserve.ErrConflict
	}

	return uint64(db.appendPart(tx, doc, newData)), nil
}

// GetDocumentKeys ...
func (db *SQLXDocumentDB) GetDocumentKeys(docID string) ([]zwibserve.Key, error) {
	var keys []zwibserve.Key

	tx := db.tx()
	defer db.commit(tx)

	rows := db.query(tx, "SELECT k.name, k.value, version FROM ZwibblerKeys k, ZwibblerDocs d WHERE d.name = ? AND k.doc = d.id", docID)

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

	doc := db.getDocByName(tx, docID)

	if doc == nil {
		return zwibserve.ErrMissing
	}

	rows := db.query(tx, "SELECT version FROM ZwibblerKeys WHERE doc = ? AND name = ?", doc.id, key.Name)
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
		db.exec(tx, `UPDATE ZwibblerKeys SET value = ?, version = ? WHERE doc = ? AND name = ?`,
			key.Value, key.Version, doc.id, key.Name)
	} else {
		db.exec(tx, `INSERT INTO ZwibblerKeys(doc, name, value, version) VALUES (?, ?, ?, ?)`,
			doc.id, key.Name, key.Value, key.Version)
	}

	return nil
}
