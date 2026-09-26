// Package lan is what sharing a project on the local network needs beyond
// the front door (sharing.feature): this machine's address there, and
// Wharf's network certificate authority for sharing over HTTPS.
//
// Both are plain Go on every OS: no tool to install, no platform branch.
package lan

import (
	"errors"
	"net"
	"net/netip"
)

// ErrNoNetwork reports that this machine has no address on a local network
// a phone could reach (sharing.feature, "Sharing needs a local network").
var ErrNoNetwork = errors.New("this machine is not connected to a local network — connect to one, then share again")

// probe is where Address pretends to send to. It is TEST-NET-1 (RFC 5737):
// never a real host, so nothing is ever reached, and routed like any other
// address, by the default route.
const probe = "192.0.2.1:9"

// Address is this machine's address on the local network: the one its
// default route leaves from. Dialling UDP sends nothing; it only asks the OS
// which address it would send from, which is the same question on every OS
// and needs no list of interfaces to guess from — a VPN, Docker's bridge or
// WSL's switch each add one.
func Address() (netip.Addr, error) {
	conn, err := net.Dial("udp4", probe)
	if err != nil {
		return netip.Addr{}, ErrNoNetwork
	}
	defer conn.Close()
	udp, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, ErrNoNetwork
	}
	addr, ok := netip.AddrFromSlice(udp.IP)
	if !ok || !Signable(addr.Unmap()) {
		return netip.Addr{}, ErrNoNetwork
	}
	return addr.Unmap(), nil
}

// Signable reports whether addr is on a private network: the only addresses
// Wharf shares on, and the only ones its network certificate authority may
// sign for. A public address would put the project on the internet.
func Signable(addr netip.Addr) bool {
	return addr.IsValid() && addr.IsPrivate()
}
