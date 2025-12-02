package database

// ExtendedTable is an example of a database table. In this case named `extended_table`.
type ExtendedTable struct {
	ID    int
	Key   string `db:"primary=yes"`
	Value string
}

// ExtendedTableFilter is used for filtering fields on database
// fetches. In this case we will only support filtering by Key.
type ExtendedTableFilter struct {
	Key *string
}
