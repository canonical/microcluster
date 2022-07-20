package types

// SQLDump represents the text of a SQL dump.
type SQLDump struct {
	Text string `json:"text" yaml:"text"`
}

// SQLQuery represents a SQL query.
type SQLQuery struct {
	Query string `json:"query" yaml:"query"`
}

// SQLBatch represents a batch of SQL results.
type SQLBatch struct {
	Results []SQLResult
}

// SQLResult represents the result of executing a SQL command.
type SQLResult struct {
	Type         string          `json:"type" yaml:"type"`
	Columns      []string        `json:"columns" yaml:"columns"`
	Rows         [][]interface{} `json:"rows" yaml:"rows"`
	RowsAffected int64           `json:"rows_affected" yaml:"rows_affected"`
}
