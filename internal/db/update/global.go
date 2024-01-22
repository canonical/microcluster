package update

import (
	"github.com/canonical/lxd/lxd/db/schema"
)

var globalSchemaUpdateManager = NewSchema()

func Schema() *SchemaUpdate {
	return globalSchemaUpdateManager.Schema()
}

func AppendSchema(extensions map[int]schema.Update) {
	globalSchemaUpdateManager.AppendSchema(extensions)
}

func SchemaDotGo() error {
	return globalSchemaUpdateManager.SchemaDotGo()
}
