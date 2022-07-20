package cluster

import (
	"database/sql"
	"fmt"
)

var stmts = map[int]string{}            // Statement code to statement SQL text
var preparedStmts = map[int]*sql.Stmt{} // Statement code to SQL statement.

// RegisterStmt register a SQL statement.
//
// Registered statements will be prepared upfront and re-used, to speed up
// execution.
//
// Return a unique registration code.
func RegisterStmt(sql string) int {
	code := len(stmts)
	stmts[code] = sql
	return code
}

// PrepareStmts prepares all registered statements and stores them in preparedStmts.
func PrepareStmts(db *sql.DB, skipErrors bool) error {
	for code, stmt := range stmts {
		preparedStmt, err := db.Prepare(stmt)
		if err != nil && !skipErrors {
			return fmt.Errorf("%q: %w", stmt, err)
		}

		preparedStmts[code] = preparedStmt
	}

	return nil
}

// stmt returns the prepared statement with the given code.
func stmt(tx *sql.Tx, code int) *sql.Stmt {
	stmt, ok := preparedStmts[code]
	if !ok {
		panic(fmt.Sprintf("No prepared statement registered with code %d", code))
	}

	return tx.Stmt(stmt)
}
