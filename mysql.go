package zwibserve

import (
	"fmt"
	_ "github.com/go-sql-driver/mysql" // include mysql driver
)

const mysqlSchema = mariadbSchema // Reuse MariaDB schema

func NewMySQLConnection(port int, host, user, password, dbname string) DocumentDB {
	mysqlInfo := fmt.Sprintf("%s:%s@(%s:%d)/%s", user, password, host, port, dbname)
	return NewSQLXConnection("mysql", mysqlInfo, mysqlSchema, 1, false)
}
