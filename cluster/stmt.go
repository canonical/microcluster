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

// Stmt prepares the in-memory prepared statement for the transaction.
func Stmt(tx *sql.Tx, code int) (*sql.Stmt, error) {
	stmt, ok := preparedStmts[code]
	if !ok {
		return nil, fmt.Errorf("No prepared statement registered with code %d", code)
	}

	return tx.Stmt(stmt), nil
}

// StmtString returns the in-memory query string with the given code.
func StmtString(code int) (string, error) {
	stmt, ok := stmts[code]
	if !ok {
		return "", fmt.Errorf("No prepared statement registered with code %d", code)
	}

	return stmt, nil
}
