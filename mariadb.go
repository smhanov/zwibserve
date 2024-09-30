package zwibserve

import (
	"fmt"
	_ "github.com/go-sql-driver/mysql" // include mysql driver
)

const mariadbSchema = `
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
    docID VARCHAR(255) PRIMARY KEY,
    lastAccess BIGINT,
    data LONGBLOB
);

CREATE TABLE IF NOT EXISTS ZwibblerKeys (
    docID VARCHAR(255),
    name VARCHAR(255),
    value VARCHAR(255),
    version INT,
    UNIQUE(docID, name),
    FOREIGN KEY (docID) REFERENCES ZwibblerDocs(docid) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ZwibblerTokens (
    tokenID VARCHAR(255) UNIQUE,
    docID VARCHAR(255),
    userID VARCHAR(255),
    permissions VARCHAR(255),
    expiration BIGINT
);

CREATE INDEX IF NOT EXISTS ZwibblerTokenUserIndex ON ZwibblerTokens(userID);
`

func NewMariaDBConnection(port int, host, user, password, dbname string) DocumentDB {
	mariaInfo := fmt.Sprintf("%s:%s@(%s:%d)/%s?multiStatements=true", user, password, host, port, dbname)
	return NewSQLXConnection("mysql", mariaInfo, mariadbSchema, 1, false)
}
