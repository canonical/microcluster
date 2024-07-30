package utils

import (
	"fmt"
	"strings"

	"github.com/canonical/lxd/shared/validate"
)

// ValidateFQDN validates that the given name is a a valid fully qualified domain name.
func ValidateFQDN(name string) error {
	// Validate length
	if len(name) < 1 || len(name) > 255 {
		return fmt.Errorf("Name must be 1-255 characters long")
	}

	hostnames := strings.Split(name, ".")
	for _, h := range hostnames {
		err := validate.IsHostname(h)
		if err != nil {
			return err
		}
	}

	return nil
}
