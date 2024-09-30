package zwibserve

import (
	"fmt"
	_ "github.com/mattn/go-sqlite3" // include sqlite3
)

const sqliteSchema = `
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid TEXT PRIMARY KEY, 
	lastAccess INTEGER,
	data BLOB
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	docID TEXT,
	name TEXT,
	value MEDIUMTEXT,
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

// NewSQLITEDB creates a new document storage based on SQLITE
func NewSQLITEDB(filename string) DocumentDB {
	return NewSQLXConnection("sqlite3", fmt.Sprintf("file:%s?_busy_timeout=5000&mode=rwc&_journal_mode=WAL&cache=shared", filename), sqliteSchema, 1, false)
}
