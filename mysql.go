package zwibserve

import (
	"fmt"
	_ "github.com/go-sql-driver/mysql" // include mysql driver
)

const mysqlSchema = mariadbSchema // Reuse MariaDB schema

func NewMySQLConnection(server, user, password, dbname string) DocumentDB {
	mysqlInfo := fmt.Sprintf("%s:%s@(%s)/%s", user, password, server, dbname)
	return NewSQLXConnection("mysql", mysqlInfo, mysqlSchema, 1, false)
}
