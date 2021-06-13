package sqlxdb

import (
	"fmt"
)

var mariaSchema = []string{`
CREATE TABLE IF NOT EXISTS ZwibblerDocs (
	id 			BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	name 		VARCHAR(256) NOT NULL UNIQUE, 
	autoClean	BOOLEAN NOT NULL DEFAULT 0,		
	lastAccess 	BIGINT NOT NULL DEFAULT 0,
	size		BIGINT NOT NULL DEFAULT 0,
	seq			BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB;
`, `
CREATE TABLE IF NOT EXISTS ZwibblerDocParts (
	doc 	BIGINT NOT NULL,
	seq 	BIGINT NOT NULL,
	data	LONGBLOB NOT NULL,
	PRIMARY KEY (doc, seq),
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
) ENGINE=InnoDB;
`, `
CREATE TABLE IF NOT EXISTS ZwibblerKeys (
	doc		BIGINT NOT NULL,
	name	VARCHAR(256) NOT NULL,
	value	TEXT NOT NULL DEFAULT "",
	version	BIGINT NOT NULL DEFAULT 0,
	UNIQUE(doc, name),
	FOREIGN KEY (doc) REFERENCES ZwibblerDocs(id) ON DELETE CASCADE
) ENGINE=InnoDB;
`,
}

const mariaDriver = "mysql"

func mariaDataSource(host string, port int, user string, password string, dbname string) string {
	return fmt.Sprintf("%s:%s@(%s:%d)/%s", user, password, host, port, dbname)
}

func NewMariaConnection(host string, port int, user string, password string, dbname string) SQLXConnection {
	return NewSQLXConnection(mariaDriver, mariaDataSource(host, port, user, password, dbname), mariaSchema, 10)
}
