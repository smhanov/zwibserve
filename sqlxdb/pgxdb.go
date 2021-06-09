package sqlxdb

import (
	"fmt"
	"github.com/smhanov/zwibserve"
)

var pgxSchema = []string{`
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid TEXT PRIMARY KEY, 
	lastAccess INTEGER,
	data BYTEA
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

const pgxDriver = "pgx"

func pgxDataSource(host string, port int, user string, password string, dbname string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, host, port, dbname)
}

func NewPgxDb(host string, port int, user string, password string, dbname string) zwibserve.DocumentDB {
	return NewSQLXDB(pgxDriver, pgxDataSource(host, port, user, password, dbname), pgxSchema)
}
