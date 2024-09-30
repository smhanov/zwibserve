package zwibserve

import (
	"fmt"
	_ "github.com/go-sql-driver/mysql" // include mysql driver
)

const mariadbSchema = `
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
    docID TEXT PRIMARY KEY,
    lastAccess BIGINT,
    data LONGBLOB
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
    docID TEXT,
    name TEXT,
    value MEDIUMTEXT,
    version INT,
    UNIQUE(docID, name),
    FOREIGN KEY (docID) REFERENCES ZwibblerDocs(docid) ON DELETE CASCADE
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

func NewMariaDBConnection(server, user, password, dbname string) DocumentDB {
	mariaInfo := fmt.Sprintf("%s:%s@(%s)/%s?multiStatements=true", user, password, server, dbname)
	return NewSQLXConnection("mysql", mariaInfo, mariadbSchema, 1, false)
}
