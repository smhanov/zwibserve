package sqlxdb

import (
	"fmt"
	"github.com/smhanov/zwibserve"
)

var sqliteSchema = []string{`
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
	version INTEGER,
	UNIQUE(docID, name),
	FOREIGN KEY (docID) REFERENCES ZwibblerDocs(docID) ON DELETE CASCADE
);
`}

const sqliteDriver = "sqlite3"

func sqliteDataSource(filename string) string {
	return fmt.Sprintf("file:%s?_busy_timeout=5000&mode=rwc&_journal_mode=WAL&cache=shared", filename)
}

func NewSqliteDb(filename string) zwibserve.DocumentDB {
	return NewSQLXDB(sqliteDriver, sqliteDataSource(filename), sqliteSchema)
}
