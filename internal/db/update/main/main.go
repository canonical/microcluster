package main

import (
	"fmt"

	"github.com/canonical/microcluster/internal/db/update"
)

func main() {
	err := update.SchemaDotGo()
	if err != nil {
		fmt.Println(err)
		return
	}

  fmt.Println("Schema updated successfully")
}
