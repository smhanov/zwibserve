package sqlxdb

import (
	"fmt"
)

var pgxSchema = []string{`
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	id 			BIGSERIAL NOT NULL PRIMARY KEY,
	name 		VARCHAR(256) NOT NULL UNIQUE, 
	autoClean	SMALLINT NOT NULL DEFAULT 0,		
	lastAccess 	BIGINT NOT NULL DEFAULT 0,
	size		BIGINT NOT NULL DEFAULT 0,
	seq			BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS ZwibblerDocParts (
	doc 	BIGINT NOT NULL,
	seq 	BIGINT NOT NULL,
	data	BYTEA NOT NULL,
	PRIMARY KEY (doc, seq),
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	doc		BIGINT NOT NULL,
	name	VARCHAR(256) NOT NULL,
	value	TEXT,
	version	INTEGER,
	UNIQUE(doc, name),
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
);
`}

const pgxDriver = "pgx"

func pgxDataSource(host string, port int, user string, password string, dbname string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, host, port, dbname)
}

func NewPgxConnection(host string, port int, user string, password string, dbname string) SQLXConnection {
	return NewSQLXConnection(pgxDriver, pgxDataSource(host, port, user, password, dbname), pgxSchema, 10)
}
