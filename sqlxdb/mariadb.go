package sqlxdb

import (
	"fmt"
	"github.com/smhanov/zwibserve"
)

var mariaSchema = []string{`
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	docid VARCHAR(256) PRIMARY KEY,
	lastAccess INTEGER,
	data LONGBLOB
) ENGINE=InnoDB
`, `
CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	docid VARCHAR(256) NOT NULL,
	name VARCHAR(256) NOT NULL,
	value TEXT,
	version INTEGER,
	PRIMARY KEY (docid, name),
	FOREIGN KEY (docid) REFERENCES ZwibblerDocs (docid) ON DELETE CASCADE
) ENGINE=InnoDB
`,
}

const mariaDriver = "mysql"

func mariaDataSource(host string, port int, user string, password string, dbname string) string {
	return fmt.Sprintf("%s:%s@(%s:%d)/%s", user, password, host, port, dbname)
}

func NewMariaDb(host string, port int, user string, password string, dbname string) zwibserve.DocumentDB {
	return NewSQLXDB(mariaDriver, mariaDataSource(host, port, user, password, dbname), mariaSchema)
}
