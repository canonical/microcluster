package types

import (
	"encoding/json"
	"math/rand"
	"net/netip"
)

// AddrPort is a wrapper for netip.AddrPort for which (json/yaml).(Marshaller/Unmarshaller) are implemented.
type AddrPort struct {
	netip.AddrPort
}

// AddrPorts is a defined type for a slice of AddrPort. This facilitates convenience functions.
type AddrPorts []AddrPort

// ParseAddrPort parses an IPv4/IPv6 address and port string into an AddrPort.
func ParseAddrPort(addrPortStr string) (AddrPort, error) {
	addrPort, err := netip.ParseAddrPort(addrPortStr)
	if err != nil {
		return AddrPort{}, err
	}

	return AddrPort{AddrPort: addrPort}, nil
}

// ParseAddrPorts parses a list of IPv4/IPv6 address and port strings into an AddrPorts.
func ParseAddrPorts(addrPortStrs []string) (AddrPorts, error) {
	var err error
	addrPorts := make(AddrPorts, len(addrPortStrs))
	for i, addrPortStr := range addrPortStrs {
		addrPorts[i], err = ParseAddrPort(addrPortStr)
		if err != nil {
			return nil, err
		}
	}

	return addrPorts, nil
}

// MarshalJSON implements json.Marshaler for the AddrPort type.
func (a AddrPort) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

// MarshalYAML implements yaml.Marshaler for the AddrPort type. Note that for yaml we just need to return a yaml
// marshallable type (if we return []byte as in the json implementation it is written as an int array).
func (a AddrPort) MarshalYAML() (any, error) {
	return a.String(), nil
}

// UnmarshalJSON implements json.Unmarshaler for AddrPort.
func (a *AddrPort) UnmarshalJSON(b []byte) error {
	var addrPortStr string
	err := json.Unmarshal(b, &addrPortStr)
	if err != nil {
		return err
	}

	*a, err = ParseAddrPort(addrPortStr)
	if err != nil {
		return err
	}

	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler for AddrPort.
func (a *AddrPort) UnmarshalYAML(unmarshal func(v any) error) error {
	var addrPortStr string
	err := unmarshal(&addrPortStr)
	if err != nil {
		return err
	}

	*a, err = ParseAddrPort(addrPortStr)
	if err != nil {
		return err
	}

	return nil
}

// Strings returns a string slice of the AddrPorts.
func (a AddrPorts) Strings() []string {
	addrPortStrs := make([]string, len(a))
	for i, addrPort := range a {
		addrPortStrs[i] = addrPort.String()
	}

	return addrPortStrs
}

// SelectRandom returns a randomly selected AddrPort from AddrPorts.
func (a AddrPorts) SelectRandom() AddrPort {
	return a[rand.Intn(len(a))]
}
