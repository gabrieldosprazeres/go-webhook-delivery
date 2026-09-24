package outboundhttp

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	Profile  config.Profile
	Resolver Resolver
}

func (p Policy) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		if !p.allowed(literal) {
			return nil, ErrPolicy
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addresses, err := resolver.LookupNetIP(resolveCtx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrPolicy
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if !address.IsValid() || address.Zone() != "" || address.Is4In6() {
			return nil, ErrPolicy
		}
		address = address.Unmap()
		if !p.allowed(address) {
			return nil, ErrPolicy
		}
		if _, ok := seen[address]; !ok {
			seen[address] = struct{}{}
			result = append(result, address)
		}
	}
	return result, nil
}

func (p Policy) allowed(address netip.Addr) bool {
	if address.Is4In6() {
		return false
	}
	address = address.Unmap()
	if !address.IsValid() {
		return false
	}
	if address.IsLoopback() {
		return p.Profile != config.ProfileProduction
	}
	if address.Is4() {
		return address.IsGlobalUnicast() &&
			(contains(globalSpecialIPv4, address) || !contains(nonGlobalIPv4, address))
	}
	return contains(allocatedIPv6, address) &&
		(contains(globalSpecialIPv6, address) || !contains(nonGlobalIPv6, address))
}

func contains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// Only prefixes currently allocated in the IANA IPv6 unicast registry are
// eligible. Unlisted portions of 2000::/3 are reserved, so they fail closed.
var allocatedIPv6 = prefixes(
	"2001::/23", "2001:200::/23", "2001:400::/23", "2001:600::/23",
	"2001:800::/22", "2001:c00::/23", "2001:e00::/23", "2001:1200::/23",
	"2001:1400::/22", "2001:1800::/23", "2001:1a00::/23", "2001:1c00::/22",
	"2001:2000::/19", "2001:4000::/23", "2001:4200::/23", "2001:4400::/23",
	"2001:4600::/23", "2001:4800::/23", "2001:4a00::/23", "2001:4c00::/23",
	"2001:5000::/20", "2001:8000::/19", "2001:a000::/20", "2001:b000::/20",
	"2002::/16", "2003::/18", "2400::/12", "2410::/12", "2600::/12",
	"2610::/23", "2620::/23", "2630::/12", "2800::/12", "2a00::/12",
	"2a10::/12", "2c00::/12",
)

// IANA special-purpose entries explicitly marked globally reachable.
var globalSpecialIPv4 = prefixes("192.0.0.9/32", "192.0.0.10/32")
var globalSpecialIPv6 = prefixes(
	"2001:1::1/128", "2001:1::2/128", "2001:1::3/128", "2001:3::/32",
	"2001:4:112::/48", "2001:20::/28", "2001:30::/28",
)

// Remaining IANA special-purpose IPv4 entries are never valid egress targets.
var nonGlobalIPv4 = prefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)

// The broad IETF block is denied first, with the globally reachable exceptions
// above. Translation, documentation and deprecated tunnel spaces stay denied.
var nonGlobalIPv6 = prefixes(
	"2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20",
)

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}
