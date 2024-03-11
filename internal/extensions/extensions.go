package extensions

import (
	"fmt"
	"regexp"
	"strings"
)

var internalExtensionRegex = regexp.MustCompile(`^internal:[a-z_0-9]+$`)
var externalExtensionRegex = regexp.MustCompile(`^[a-z_0-9]+$`)

type invalidInternalExtensionErr struct {
	invalidExt string
}

func (e *invalidInternalExtensionErr) Error() string {
	return fmt.Sprintf("Internal extension format is invalid (%v). It should be in the format `internal:<extension>` with only lowercase letters and underscores", e.invalidExt)
}

type invalidExternalExtensionErr struct {
	invalidExt string
}

func (e *invalidExternalExtensionErr) Error() string {
	return fmt.Sprintf("External extension format is invalid (%v). It should be in the format `<extension>` with only lowercase letters and underscores", e.invalidExt)
}

// Extensions represents a registry of extensions.
type Extensions struct {
	// internal extensions are related to MicroCluster capabilities, not capabilities of
	// MicroCluster-backed services.
	//
	// To distinguish between internal and external extensions, internal extensions
	// are prefixed with a `internal:` value.
	// e.g, `internal:runtime_extension_v1`, `internal:runtime_extension_v2`, ...
	internal map[string]struct{}

	// External extensions are related to capabilities of MicroCluster-backed services.
	// e.g, `microovn_custom_encapsulation_ip`
	external map[string]struct{}
}

// validateExternalExtension validates the given external extension.
func validateExternalExtension(extension string) error {
	if extension == "" {
		return fmt.Errorf("Extension cannot be empty")
	}

	// Aside from the internal extensions, we let the extension to be like
	// `<extension>` with only lowercase letters and underscores.
	if !externalExtensionRegex.MatchString(extension) {
		return &invalidExternalExtensionErr{invalidExt: extension}
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
		return &invalidInternalExtensionErr{invalidExt: extension}
	}

	return nil
}

// HasExtension reports whether the extension set supports the given extension.
func (e *Extensions) HasExtension(ext string) bool {
	_, ok := e.external[ext]
	if ok {
		return ok
	}

	_, ok = e.internal[ext]
	return ok
}

// Version returns the number of extensions in the set, representing its version number.
func (e *Extensions) Version() int {
	return len(e.internal) + len(e.external)
}

// NewExtensionRegistry creates a new instance of the Extensions struct, which represents a registry of extensions.
// We have the option to populate the internal extensions or not. Having s option becomes handy
// when we want to create a custom registry of extensions (see `NewExtensionRegistryFromList`)
func NewExtensionRegistry(populateInternal bool) (*Extensions, error) {
	internal := make(map[string]struct{})
	if populateInternal {
		// Populate internal extensions here.
		internal["internal:runtime_extension_v1"] = struct{}{}
	}

	var err error
	for internalExtension := range internal {
		err = validateInternalExtension(internalExtension)
		if err != nil {
			return nil, err
		}
	}

	return &Extensions{
		internal: internal,
		external: make(map[string]struct{}),
	}, nil
}

// NewExtensionRegistryFromList creates a new instance of the Extensions struct, from a list of extensions.
// The passed extensions could be internal or external or just invalid.
// The function is typically used to create a registry from an API response in order to compare
// the source and the target systems' extensions for compatibility.
func NewExtensionRegistryFromList(extensions []string) (*Extensions, error) {
	registry, err := NewExtensionRegistry(false)
	if err != nil {
		return nil, err
	}

	for _, extension := range extensions {
		err1 := validateExternalExtension(extension)
		if err1 != nil {
			// The extension could be an internal extension.
			err2 := validateInternalExtension(extension)
			if err2 != nil {
				// The extension is invalid in both case.
				return nil, fmt.Errorf("Extension %q is invalid", extension)
			}

			registry.internal[extension] = struct{}{}
		} else {
			registry.external[extension] = struct{}{}
		}
	}

	return registry, nil
}

// Register registers new external extensions to the Extensions struct.
func (e *Extensions) Register(newExtensions []string) error {
	// Check for duplicates for internal and External extensions
	for _, newExtension := range newExtensions {
		_, found := e.external[newExtension]
		if found {
			return fmt.Errorf("Extension %q already registered", newExtension)
		}

		err := validateExternalExtension(newExtension)
		if err != nil {
			return err
		}

		e.external[newExtension] = struct{}{}
	}

	return nil
}

// SerializeForAPI serializes the Extensions into a slice of strings.
// It combines the internal and external extensions and returns them as a single slice.
func (e *Extensions) SerializeForAPI() []string {
	extensions := make([]string, 0, len(e.internal)+len(e.external))
	for extension := range e.internal {
		extensions = append(extensions, extension)
	}

	for extension := range e.external {
		extensions = append(extensions, extension)
	}

	return extensions
}

// SerializeForDB serializes the Extensions struct for database storage.
// It returns two strings: internal and external.
func (e *Extensions) SerializeForDB() (internal, external string) {
	var internalKeys, externalKeys []string
	for extension := range e.internal {
		internalKeys = append(internalKeys, extension)
	}

	for extension := range e.external {
		externalKeys = append(externalKeys, extension)
	}

	return strings.Join(internalKeys, ","), strings.Join(externalKeys, ",")
}

// IsSameVersion checks if the source registry supports the target registry.
func (e *Extensions) IsSameVersion(t *Extensions) error {
	if t == nil {
		return fmt.Errorf("The target extension registry is nil")
	}

	// First, check if the number of internal and external extensions are the same.
	if len(e.internal) != len(t.internal) || len(e.external) != len(t.external) {
		return fmt.Errorf("The source and target registries have different number of extensions")
	}

	// Then, check if the internal extensions are the same.
	for srcInternalExt := range e.internal {
		_, found := t.internal[srcInternalExt]
		if !found {
			return fmt.Errorf("The target registry does not support the internal extension %q", srcInternalExt)
		}
	}

	// Finally, check if the external extensions are the same.
	for srcExternalExt := range e.external {
		_, found := t.external[srcExternalExt]
		if !found {
			return fmt.Errorf("The target registry does not support the external extension %q", srcExternalExt)
		}
	}

	return nil
}
