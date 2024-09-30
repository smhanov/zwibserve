package zwibserve

import (
	"fmt"
	_ "github.com/lib/pq" // include postgresql driver
)

const postgresqlSchema = `
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid TEXT PRIMARY KEY,
	lastAccess BIGINT,
	data BYTEA
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	docID TEXT,
	name TEXT,
	value MEDIUMTEXT,
	version INT,
	UNIQUE(docID, name),
	FOREIGN KEY (docID) REFERENCES ZwibblerDocs(docID) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ZwibblerTokens (
	tokenID TEXT UNIQUE,
	docID TEXT,
	userID TEXT,
	permissions TEXT,
	expiration BIGINT
);

CREATE INDEX IF NOT EXISTS ZwibblerTokenUserIndex ON ZwibblerTokens(userID);
`

func NewPostgreSQLConnection(server, user, password, dbname string) DocumentDB {
	psqlInfo := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", user, password, server, dbname)
	return NewSQLXConnection("postgres", psqlInfo, postgresqlSchema, 1, true)
}
