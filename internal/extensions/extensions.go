package extensions

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/canonical/lxd/shared"
)

var internalExtensionRegex = regexp.MustCompile(`^internal:[a-z0-9]+(_[a-z0-9]+)*$`)
var externalExtensionRegex = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// Extensions represents a registry of extensions.
//
// `internal` extensions are related to MicroCluster capabilities, not capabilities of
// MicroCluster-backed services.
//
// To distinguish between `internal` and `external` extensions, `internal` extensions
// are prefixed with a `internal:` value.
// e.g, `internal:runtime_extension_v1`, `internal:runtime_extension_v2`, ...
//
// External extensions are related to capabilities of MicroCluster-backed services.
// e.g, `microovn_custom_encapsulation_ip`
type Extensions []string

// Populate internal extensions here.
var internalExtensions = Extensions{
	"internal:runtime_extension_v1",
}

// validateExternalExtension validates the given external extension.
func validateExternalExtension(extension string) error {
	if extension == "" {
		return fmt.Errorf("Extension cannot be empty")
	}

	// Aside from the internal extensions, we let the extension to be like
	// `<extension>` with only lowercase letters and underscores.
	if !externalExtensionRegex.MatchString(extension) {
		return fmt.Errorf("External extension name %q is invalid: Extension name must contain only lowercase letters and underscores, and must not begin or end with an underscore", extension)
	}

	return nil
}

// validateInternalExtension validates the given internal extension.
func validateInternalExtension(extension string) error {
	if extension == "" {
		return fmt.Errorf("Extension cannot be empty")
	}

	// Internal extensions should be in the format `internal:<extension>` with only lowercase letters and underscores.
	if !internalExtensionRegex.MatchString(extension) {
		return fmt.Errorf("Internal extension name %q is invalid: Extension name must contain only lowercase letters and underscores, and must begin by `internal:`", extension)
	}

	return nil
}

// HasExtension reports whether the extension set supports the given extension.
func (e Extensions) HasExtension(ext string) bool {
	for _, extension := range e {
		if extension == ext {
			return true
		}
	}

	return false
}

// Version returns the number of extensions in the set, representing its version number.
func (e Extensions) Version() int {
	return len(e)
}

// NewExtensionRegistry creates a new instance of the Extensions struct, which represents a registry of extensions.
// We have the option to populate the internal extensions or not. Having s option becomes handy
// when we want to create a custom registry of extensions (see `NewExtensionRegistryFromList`)
func NewExtensionRegistry(populateInternal bool) (Extensions, error) {
	extensions := Extensions{}
	var err error
	if populateInternal {
		extensions = make(Extensions, 0, len(internalExtensions))
		for _, internalExtension := range internalExtensions {
			err = validateInternalExtension(internalExtension)
			if err != nil {
				return nil, err
			}

			extensions = append(extensions, internalExtension)
		}
	}

	return extensions, nil
}

// NewExtensionRegistryFromList creates a new instance of the Extensions struct, from a list of extensions.
// The passed extensions could be internal or external or just invalid.
// The function is typically used to create a registry from an API response in order to compare
// the source and the target systems' extensions for compatibility.
// The returned registry is always sorted with internal extensions first.
func NewExtensionRegistryFromList(extensions []string) (Extensions, error) {
	registry, err := NewExtensionRegistry(false)
	if err != nil {
		return nil, err
	}

	for _, extension := range extensions {
		err = validateExternalExtension(extension)
		if err != nil {
			// The extension could be an internal extension.
			err = validateInternalExtension(extension)
			if err != nil {
				// The extension is invalid in both case.
				return nil, fmt.Errorf("Extension %q is invalid", extension)
			}

			// The extension is an internal extension.
			registry = append(registry, extension)
		} else {
			// The extension is an external extension.
			registry = append(registry, extension)
		}
	}

	return registry, nil
}

// Register registers new external extensions to the Extensions struct.
func (e *Extensions) Register(newExtensions []string) error {
	// Check for duplicates for internal and External extensions
	for _, extension := range newExtensions {
		if shared.ValueInSlice[string](extension, *e) {
			return fmt.Errorf("Extension %q already registered", extension)
		}

		err := validateExternalExtension(extension)
		if err != nil {
			return err
		}

		*e = append(*e, extension)
	}

	return nil
}

// Value implements the driver.Valuer interface to serialize the Extensions struct for database storage.
func (e Extensions) Value() (driver.Value, error) {
	if len(e) == 0 {
		return "[]", nil
	}

	return json.Marshal(e)
}

// Scan implements the sql.Scanner interface to deserialize the Extensions struct from database storage.
func (e *Extensions) Scan(value any) error {
	if value == nil {
		*e = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("type assertion to []byte or string failed, incompatible type (%T) for value: %v", value, value)
	}

	return json.Unmarshal(bytes, e)
}

// IsSameVersion checks if the source registry supports the target registry.
func (e Extensions) IsSameVersion(t Extensions) error {
	// First, check if the number of extensions are the same.
	if len(e) != len(t) {
		return fmt.Errorf("The source (%v) and target (%v) registries have different number of extensions", e, t)
	}

	// Then, check if the extensions are the same.
	// The two extensions are already sorted so we can compare by index directly.
	for i := range e {
		if (e)[i] != (t)[i] {
			return fmt.Errorf("The source and target registries have different extensions")
		}
	}

	return nil
}
