package sqlxdb

import (
	"database/sql"
	"github.com/jmoiron/sqlx"
	"log"
)

type SQLXConnection interface {
	Tx() *sqlx.Tx
	Query(tx *sqlx.Tx, query string, args ...interface{}) *sql.Rows
	Exec(tx *sqlx.Tx, query string, args ...interface{})
	Commit(tx *sqlx.Tx)
	Close(rows *sql.Rows)
	Scan(rows *sql.Rows, fields ...interface{})
}

type sqlxConnectionImpl struct {
	conn *sqlx.DB
}

func NewSQLXConnection(driverName, dataSourceName string, schema []string, maxOpenConnections int) SQLXConnection {
	sqldb, err := sqlx.Connect(driverName, dataSourceName)

	if err != nil {
		log.Panic(err)
	}

	sqldb.SetMaxOpenConns(maxOpenConnections)

	for i := 0; i < len(schema); i++ {
		sqldb.MustExec(schema[i])
	}

	db := &sqlxConnectionImpl{
		conn: sqldb,
	}

	return db
}

func (db *sqlxConnectionImpl) Tx() *sqlx.Tx {
	return db.conn.MustBegin()
}

func (db *sqlxConnectionImpl) Query(tx *sqlx.Tx, query string, args ...interface{}) *sql.Rows {
	rows, err := tx.Query(db.conn.Rebind(query), args...)
	if err != nil {
		log.Panic(err)
	}
	return rows
}

func (db *sqlxConnectionImpl) Exec(tx *sqlx.Tx, query string, args ...interface{}) {
	tx.MustExec(db.conn.Rebind(query), args...)
}

func (db *sqlxConnectionImpl) Commit(tx *sqlx.Tx) {
	err := tx.Commit()
	if err != nil {
		log.Fatal(err)
	}
}

func (db *sqlxConnectionImpl) Close(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func (db *sqlxConnectionImpl) Scan(rows *sql.Rows, fields ...interface{}) {
	err := rows.Scan(fields...)
	if err != nil {
		log.Panic(err)
	}
}
