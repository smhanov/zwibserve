package sqlxdb

import (
	"github.com/smhanov/zwibserve"
	"time"

	"github.com/jmoiron/sqlx"
)

func now() int64 {
	return time.Now().Unix()
}

// SQLXDocumentDB is a document database using SQLX. The documents are all stored in a single file database.
type SQLXDocumentDB struct {
	SQLXConnection
	lastClean  time.Time
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
func NewSQLXDB(sqldb SQLXConnection) zwibserve.DocumentDB {

	db := &SQLXDocumentDB{
		sqldb,
		time.Now(),
		0,
	}

	db.clean()
	return db
}

// SetExpiration ...
func (db *SQLXDocumentDB) SetExpiration(seconds int64) {
	db.expiration = seconds
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

	tx := db.Tx()
	defer db.Commit(tx)

	db.Exec(tx, "DELETE FROM ZwibblerDocs WHERE lastAccess < ? AND autoClean > 0",
		now.Unix()-seconds)
	db.lastClean = now
}

func (db *SQLXDocumentDB) getDocByName(tx *sqlx.Tx, name string) *Doc {
	rows := db.Query(tx, "SELECT id, name, autoClean, lastAccess, size, seq FROM ZwibblerDocs WHERE name = ?", name)
	var res *Doc = nil
	if rows.Next() {
		res = &Doc{}
		db.Scan(rows, &res.id, &res.name, &res.autoClean, &res.lastAccess, &res.size, &res.seq)
	}
	db.Close(rows)
	return res
}

func (db *SQLXDocumentDB) appendPart(tx *sqlx.Tx, doc *Doc, data []byte) int64 {
	l := int64(len(data))
	if l > 0 {
		seq := doc.seq + 1
		size := doc.size + l
		db.Exec(tx, "INSERT INTO ZwibblerDocParts (doc, seq, data) VALUES (?, ?, ?)", doc.id, seq, data)
		db.Exec(tx, "UPDATE ZwibblerDocs set lastAccess = ?, size = ?, seq = ? WHERE id = ?", now(), size, seq, doc.id)
		return size
	} else {
		return doc.size
	}
}

// GetDocument ...
func (db *SQLXDocumentDB) GetDocument(docID string, mode zwibserve.CreateMode, initialData []byte) ([]byte, bool, error) {
	db.clean()

	tx := db.Tx()
	defer db.Commit(tx)

	doc := db.getDocByName(tx, docID)

	if doc == nil && mode == zwibserve.NeverCreate {
		return nil, false, zwibserve.ErrMissing
	} else if doc != nil && mode == zwibserve.AlwaysCreate {
		return nil, false, zwibserve.ErrExists
	} else {
		created := false
		if doc == nil {
			db.Exec(tx, "INSERT INTO ZwibblerDocs (name, lastAccess, autoClean, size, seq) VALUES (?, ?, ?, ?, ?)",
				docID, now(), 1, 0, 0)
			doc = db.getDocByName(tx, docID)
			db.appendPart(tx, doc, initialData)
			created = true
		} else {
			db.Exec(tx, "UPDATE ZwibblerDocs set lastAccess = ? WHERE id = ?", now(), doc.id)
		}

		rows := db.Query(tx, "SELECT data FROM ZwibblerDocParts WHERE doc = ? ORDER BY seq", doc.id)
		var data []byte
		for rows.Next() {
			var rowData []byte
			db.Scan(rows, &rowData)
			data = append(data, rowData...)
			created = false
		}
		db.Close(rows)

		return data, created, nil
	}
}

// AppendDocument ...
func (db *SQLXDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	db.clean()
	tx := db.Tx()
	defer db.Commit(tx)

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

	tx := db.Tx()
	defer db.Commit(tx)

	rows := db.Query(tx, "SELECT k.name, k.value, version FROM ZwibblerKeys k, ZwibblerDocs d WHERE d.name = ? AND k.doc = d.id", docID)

	for rows.Next() {
		var key zwibserve.Key
		db.Scan(rows, &key.Name, &key.Value, &key.Version)
		keys = append(keys, key)
	}
	db.Close(rows)

	return keys, nil
}

// SetDocumentKey ...
func (db *SQLXDocumentDB) SetDocumentKey(docID string, oldVersion int, key zwibserve.Key) error {
	tx := db.Tx()
	defer db.Commit(tx)

	var dbVersion int
	exists := false

	doc := db.getDocByName(tx, docID)

	if doc == nil {
		return zwibserve.ErrMissing
	}

	rows := db.Query(tx, "SELECT version FROM ZwibblerKeys WHERE doc = ? AND name = ?", doc.id, key.Name)
	if rows.Next() {
		exists = true
		db.Scan(rows, &dbVersion)
	}
	db.Close(rows)

	// if the key exists, and the old version does not match, then fail.
	if exists && dbVersion != oldVersion {
		return zwibserve.ErrConflict
	} else if !exists && oldVersion != 0 {
		return zwibserve.ErrConflict
	}

	// if the key exists, perform update. otherwise, perform insert.
	if exists {
		db.Exec(tx, `UPDATE ZwibblerKeys SET value = ?, version = ? WHERE doc = ? AND name = ?`,
			key.Value, key.Version, doc.id, key.Name)
	} else {
		db.Exec(tx, `INSERT INTO ZwibblerKeys(doc, name, value, version) VALUES (?, ?, ?, ?)`,
			doc.id, key.Name, key.Value, key.Version)
	}

	return nil
}
