package sqlxdb

import (
	"fmt"
)

var sqliteSchema = []string{`
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	id 			INTEGER PRIMARY KEY,
	name 		TEXT UNIQUE, 
	autoClean	INTEGER,		
	lastAccess 	INTEGER,
	size		INTEGER,
	seq			INTEGER
);

CREATE TABLE IF NOT EXISTS ZwibblerDocParts (
	doc 	INTEGER NOT NULL,
	seq 	INTEGER	NOT NULL,
	data	BLOB,
	PRIMARY KEY (doc, seq)
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	doc		INTEGER NOT NULL,
	name	TEXT NOT NULL,
	value	TEXT,
	version	INTEGER,
	UNIQUE(doc, name),
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
);
`}

const sqliteDriver = "sqlite3"

func sqliteDataSource(filename string) string {
	return fmt.Sprintf("file:%s?_busy_timeout=5000&mode=rwc&_journal_mode=WAL&cache=shared", filename)
}

func NewSqliteConnection(filename string) SQLXConnection {
	return NewSQLXConnection(sqliteDriver, sqliteDataSource(filename), sqliteSchema, 1)
}
