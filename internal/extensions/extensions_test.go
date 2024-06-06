package extensions

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateExternalExtension(t *testing.T) {
	cases := []struct {
		name      string
		extension string
		wantError bool
	}{
		{"Valid Extension", "valid_extension", false},
		{"Valid Extension with multiple underscores", "a_0_a_0_a_0", false},
		{"Valid single-character Extension", "a", false},
		{"Valid single-character Extension with one underscore", "a_0", false},
		{"Empty Extension", "", true},
		{"Valid Start Numeric", "123_invalid", false},
		{"Invalid Characters", "Invalid-Extension!", true},
		{"Invalid Start Underscore", "_invalidextension", true},
		{"Invalid End Underscore", "invalidextension_", true},
		{"Invalid Non Alphanumeric", "invalid-extension", true},
		{"Invalid Start Underscore", "_invalid_123", true},
		{"Invalid sequential underscores", "a__0", true},
		{"Invalid only underscore", "_", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateExternalExtension(c.extension)
			if c.wantError {
				assert.Error(t, err, "Expected an error for invalid extension")
			} else {
				assert.NoError(t, err, "Expected no error for valid extension")
			}
		})
	}
}

func TestValidateInternalExtension(t *testing.T) {
	cases := []struct {
		name      string
		extension string
		wantError bool
	}{
		{"Valid Internal Extension", "internal:runtime_extension_v1", false},
		{"Valid Internal Extension with multiple underscores", "internal:a_0_a_0_a_0", false},
		{"Valid Internal single-character Extension", "internal:a", false},
		{"Valid Internal single-character Extension with one underscore", "internal:a_0", false},
		{"Missing Prefix", "runtime_extension_v1", true},
		{"Empty Extension", "", true},
		{"Invalid Format", "internal:_invalid_extension", true},
		{"Valid with Numbers", "internal:valid_extension_123", false},
		{"Invalid Character", "internal:invalid-extension", true},
		{"Invalid sequential underscores", "internal:a__0", true},
		{"Invalid only underscore", "internal:_", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateInternalExtension(c.extension)
			if c.wantError {
				assert.Error(t, err, "Expected an error for invalid internal extension")
			} else {
				assert.NoError(t, err, "Expected no error for valid internal extension")
			}
		})
	}
}

func TestNewExtensionRegistry(t *testing.T) {
	registry, err := NewExtensionRegistry(true)
	assert.NoError(t, err)
	assert.NotNil(t, registry)
	assert.Contains(t, registry, "internal:runtime_extension_v1", "Should contain internal extensions")
}

func TestNewExtensionRegistryFromList(t *testing.T) {
	extensions := []string{"internal:runtime_extension_v1", "valid_extension"}
	registry, err := NewExtensionRegistryFromList(extensions)
	assert.NoError(t, err)
	assert.NotNil(t, registry)
	assert.Equal(t, 2, len(registry))
	assert.Contains(t, registry, "internal:runtime_extension_v1")
	assert.Contains(t, registry, "valid_extension")
}

func TestJSONSerialization(t *testing.T) {
	registry := Extensions{"internal:runtime_extension_v1", "valid_extension"}
	data, err := json.Marshal(registry)
	assert.NoError(t, err)

	var newRegistry Extensions
	err = json.Unmarshal(data, &newRegistry)
	assert.NoError(t, err)
	assert.Equal(t, registry, newRegistry)
}

func TestIsSameVersion(t *testing.T) {
	registry1 := Extensions{"internal:runtime_extension_v1", "valid_extension"}
	registry2 := Extensions{"internal:runtime_extension_v1", "valid_extension"}
	registry3 := Extensions{"internal:runtime_extension_v1"}

	err := registry1.IsSameVersion(registry2)
	assert.NoError(t, err)

	err = registry1.IsSameVersion(registry3)
	assert.Error(t, err)
}

func TestRegisterALotOfExtensions(t *testing.T) {
	registry, _ := NewExtensionRegistry(false)
	for i := 0; i < 10000; i++ {
		ext := fmt.Sprintf("valid_extension_%d", i)
		err := registry.Register([]string{ext})
		if err != nil {
			t.Fatalf("Failed to register extension: %s, error: %v", ext, err)
		}
	}

	if len(registry) != 10000 {
		t.Errorf("Expected 10000 extensions, got %d", len(registry))
	}
}

func TestExtensionsValuerAndScanner(t *testing.T) {
	var err error
	db, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)

	defer db.Close() //nolint:errcheck // Not relevant for the test.

	_, err = db.Exec("CREATE TABLE core_cluster_members (api_extensions TEXT NOT NULL DEFAULT '[]')")
	require.NoError(t, err)

	exts := Extensions{"internal:runtime_extension_v1", "microovn_custom_encapsulation_ip"}
	res, err := db.Exec("INSERT INTO core_cluster_members (api_extensions) VALUES (?)", exts)
	assert.NoError(t, err)
	n, err := res.RowsAffected()
	assert.NoError(t, err)
	assert.Equal(t, n, int64(1))

	// Retrieve the data
	var retrievedExts Extensions
	row := db.QueryRow("SELECT api_extensions FROM core_cluster_members")
	err = row.Scan(&retrievedExts)
	assert.NoError(t, err)

	// Check if the retrieved data is equal to the original data
	err = exts.IsSameVersion(retrievedExts)
	assert.NoError(t, err)
}
