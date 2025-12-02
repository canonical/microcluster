package types

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddrPortWithZone(t *testing.T) {
	cases := []struct {
		name     string
		addrPort AddrPort
		zone     string
		want     AddrPort
	}{
		{
			name: "IPv4",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("10.0.0.10"),
				9090,
			)},
			zone: "",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("10.0.0.10"),
				9090,
			)},
		},
		{
			name: "IPv4WithAddedZone",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("10.0.0.10"),
				9090,
			)},
			zone: "eth0",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("10.0.0.10"),
				9090,
			)},
		},
		{
			name: "IPv6",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc"),
				9090,
			)},
			zone: "",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc"),
				9090,
			)},
		},
		{
			name: "IPv6WithOriginalZone",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc%eth0"),
				9090,
			)},
			zone: "",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc"),
				9090,
			)},
		},
		{
			name: "IPv6WithAddedZone",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc"),
				9090,
			)},
			zone: "eth0",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc%eth0"),
				9090,
			)},
		},
		{
			name: "IPv6ChangeZone",
			addrPort: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc%wlp2s0"),
				9090,
			)},
			zone: "eth0",
			want: AddrPort{AddrPort: netip.AddrPortFrom(
				netip.MustParseAddr("fe80::b73c:ca5c:8035:23dc%eth0"),
				9090,
			)},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, c.addrPort.WithZone(c.zone))
		})
	}
}
