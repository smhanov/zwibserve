package zwibserve

import (
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type SQLxDocumentDB struct {
	lastClean  time.Time
	conn       *sqlx.DB
	expiration int64
}

var shouldRebind = false

func rebindQuery(query string) string {
	if shouldRebind {
		return sqlx.Rebind(sqlx.DOLLAR, query)
	}
	return query
}

func NewSQLXConnection(driverName, dataSourceName string, schema string, maxOpenConnections int, rebind bool) DocumentDB {
	shouldRebind = rebind
	sqldb, err := sqlx.Connect(driverName, dataSourceName)

	if err != nil {
		log.Panic(err)
	}

	sqldb.SetMaxOpenConns(maxOpenConnections)

	sqldb.MustExec(schema)

	db := &SQLxDocumentDB{
		conn: sqldb,
	}

	db.clean()
	return db
}

// SetExpiration ...
func (db *SQLxDocumentDB) SetExpiration(seconds int64) {
	log.Printf("SetExpiration")
	db.expiration = seconds
}

func (db *SQLxDocumentDB) CheckHealth() error {
	log.Printf("CheckHealth")
	tx := db.conn.MustBegin()
	return tx.Commit()
}

func (db *SQLxDocumentDB) clean() {
	log.Printf("clean")
	seconds := db.expiration
	if seconds == 0 || seconds == NoExpiration {
		return
	}

	now := time.Now()
	if time.Since(db.lastClean).Minutes() < 60 {
		return
	}

	db.conn.MustExec(rebindQuery("DELETE FROM ZwibblerDocs WHERE lastAccess < ?"), now.Unix()-seconds)
	db.conn.MustExec(rebindQuery("DELETE FROM ZwibblerTokens WHERE expiration < ?"), now.Unix())
	db.lastClean = now
}

// GetDocument ...
func (db *SQLxDocumentDB) GetDocument(docID string, mode CreateMode, initialData []byte) ([]byte, bool, error) {
	log.Printf("GetDocument")
	db.clean()

	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query(rebindQuery("SELECT data FROM ZwibblerDocs WHERE docid=?"), docID)
	if err != nil {
		tx.Rollback()
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
	rows.Close() // Necessary for postgresql

	if doc == nil && mode == NeverCreate {
		return nil, false, ErrMissing
	} else if doc != nil && mode == AlwaysCreate {
		return nil, false, ErrExists
	}

	if doc == nil {
		doc = initialData
		tx.MustExec(rebindQuery("INSERT INTO ZwibblerDocs (docid, lastAccess, data) VALUES (?, ?, ?)"), docID, time.Now().Unix(), doc)
	} else {
		tx.MustExec(rebindQuery("UPDATE ZwibblerDocs set lastAccess=? WHERE docid=?"), time.Now().Unix(), docID)
	}

	return doc, created, nil

}

// AppendDocument ...
func (db *SQLxDocumentDB) AppendDocument(docID string, oldLength uint64, newData []byte) (uint64, error) {
	log.Printf("AppendDocument")
	db.clean()
	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query(rebindQuery("SELECT data FROM ZwibblerDocs WHERE docid=?"), docID)
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
	rows.Close() // Necessary for postgresql

	if doc == nil {
		return 0, ErrMissing
	}

	if uint64(len(doc)) != oldLength {
		return uint64(len(doc)), ErrConflict
	}

	doc = append(doc, newData...)
	tx.MustExec(rebindQuery("UPDATE ZwibblerDocs SET data=? WHERE docid=?"), doc, docID)
	tx.MustExec(rebindQuery("UPDATE ZwibblerDocs SET lastAccess=? WHERE docid=?"), time.Now().Unix(), docID)

	return uint64(len(doc)), nil
}

// GetDocumentKeys ...
func (db *SQLxDocumentDB) GetDocumentKeys(docID string) ([]Key, error) {
	log.Printf("GetDocumentKeys")
	var keys []Key

	tx := db.conn.MustBegin()
	defer tx.Commit()

	rows, err := tx.Query(rebindQuery("SELECT name, value, version FROM ZwibblerKeys WHERE docid=?"), docID)
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
func (db *SQLxDocumentDB) SetDocumentKey(docID string, oldVersion int, key Key) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()

	var dbVersion int
	exists := false

	rows, err := tx.Query(rebindQuery("SELECT version FROM ZwibblerKeys WHERE docid=? AND name=?"), docID, key.Name)
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
	rows.Close() // Necessary for postgresql

	// if the key exists, and the old version does not match, then fail.
	if exists && dbVersion != oldVersion {
		return ErrConflict
	} else if !exists && oldVersion != 0 {
		return ErrConflict
	}

	// if the key exists, perform update. otherwise, perform insert.
	if exists {
		tx.MustExec(rebindQuery("UPDATE ZwibblerKeys SET value=?, version=? WHERE docID=? AND name=?"),
			key.Value, key.Version, docID, key.Name)
	} else {
		tx.MustExec(rebindQuery( "INSERT INTO ZwibblerKeys(docID, name, value, version) VALUES (?, ?, ?, ?)"),
			docID, key.Name, key.Value, key.Version)
	}

	return nil
}

func (db *SQLxDocumentDB) DeleteDocument(docID string) error {
	db.conn.MustExec(rebindQuery("DELETE FROM ZwibblerDocs WHERE docID=?"), docID)
	return nil
}

// AddToken ...
func (db *SQLxDocumentDB) AddToken(tokenID, docID, userID, permissions string, expirationSeconds int64, contents []byte) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()

	// check if doc exists
	if len(contents) > 0 {
		rows, err := tx.Query(rebindQuery("SELECT data FROM ZwibblerDocs WHERE docid=?"), docID)
		if err != nil {
			log.Panic(err)
		}
		defer rows.Close()
		if rows.Next() {
			return ErrConflict
		}

		tx.MustExec(rebindQuery("INSERT INTO ZwibblerDocs (docid, lastAccess, data) VALUES (?, ?, ?)"),
			docID, time.Now().Unix(), contents)
	}

	_, err := tx.Exec(rebindQuery("INSERT INTO ZwibblerTokens(tokenID, docID, userID, permissions, expiration) VALUES (?, ?, ?, ?, ?)"),
		tokenID, docID, userID, permissions, expirationSeconds)

	if err != nil {
		tx.Rollback()
		if strings.Contains(err.Error(), "UNIQUE") {
			err = ErrExists
		}
	}

	return err
}

func (db *SQLxDocumentDB) GetToken(token string) (docID, userID, permissions string, err error) {
	tx := db.conn.MustBegin()
	defer tx.Commit()
	now := time.Now().Unix()
	rows, err := tx.Query(rebindQuery("SELECT docID, userID, permissions FROM ZwibblerTokens WHERE tokenID=? AND expiration > ?"),
		token, now)
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

func (db *SQLxDocumentDB) UpdateUser(userID, permissions string) error {
	tx := db.conn.MustBegin()
	defer tx.Commit()
	tx.MustExec(rebindQuery("UPDATE ZwibblerTokens SET permissions=? WHERE userID=?"), userID, permissions)
	return nil
}
