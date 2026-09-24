package outboundhttp

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

type resolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func TestParseCanonicalizesIDNAndRejectsAmbiguity(t *testing.T) {
	got, err := Parse(config.ProfileProduction, false, "https://BÜCHER.example./hook")
	if err != nil || got.Host != "xn--bcher-kva.example" || got.Port != 443 {
		t.Fatalf("destination=%+v err=%v", got, err)
	}
	invalid := []string{"https://user@example.com/x", "https://example.com/x?q=1", "https://example.com/x#f", "https://[fe80::1%25en0]/x", "https://[::ffff:8.8.8.8]/x", "https://2130706433/x", "http://example.com/x"}
	for _, raw := range invalid {
		if _, err := Parse(config.ProfileProduction, false, raw); !errors.Is(err, ErrPolicy) {
			t.Errorf("%q err=%v", raw, err)
		}
	}
}

func TestPolicyFailsIfAnyResolvedAddressIsForbidden(t *testing.T) {
	policy := Policy{Profile: config.ProfileProduction, Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("169.254.169.254")}, nil
	})}
	if _, err := policy.Resolve(context.Background(), "example.com"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("err=%v", err)
	}
}

func TestPolicyBlocksSpecialIPv4IPv6AndMapped(t *testing.T) {
	for _, value := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.0.0.8", "192.0.0.170", "192.0.2.1", "192.88.99.1",
		"192.168.0.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1",
		"240.0.0.1", "255.255.255.255", "::", "::1", "::7f00:1", "::ffff:127.0.0.1", "::ffff:8.8.8.8",
		"64:ff9b::a9fe:a9fe", "64:ff9b:1::7f00:1", "100::1", "100:0:0:1::1",
		"2001::1", "2001:2::1", "2001:10::1", "2001:db8::1", "2002::1",
		"2d00::1", "2e00::1", "3000::1", "3800::1", "3c00::1", "3e00::1",
		"3f00::1", "3f80::1", "3fc0::1", "3fe0::1", "3ff0::1", "3ff8::1",
		"3ffc::1", "3ffe::1", "3fff::1", "5f00::1", "fc00::1", "fe80::1",
		"ff00::1", "4000::1",
	} {
		if _, err := (Policy{Profile: config.ProfileProduction}).Resolve(context.Background(), value); !errors.Is(err, ErrPolicy) {
			t.Errorf("%s err=%v", value, err)
		}
	}
}

func TestPolicyAllowsOrdinaryAndIANARegisteredGlobalAddresses(t *testing.T) {
	for _, value := range []string{
		"8.8.8.8", "192.0.0.9", "192.0.0.10", "192.31.196.1", "192.52.193.1",
		"192.175.48.1", "2001:1::1", "2001:1::2", "2001:1::3", "2001:3::1",
		"2001:4:112::1", "2001:20::1", "2001:30::1", "2606:4700:4700::1111",
	} {
		if _, err := (Policy{Profile: config.ProfileProduction}).Resolve(context.Background(), value); err != nil {
			t.Errorf("%s err=%v", value, err)
		}
	}
}

func TestPolicyFailsClosedForMixedIPv4IPv6Resolution(t *testing.T) {
	policy := Policy{Profile: config.ProfileProduction, Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("64:ff9b::a9fe:a9fe")}, nil
	})}
	if _, err := policy.Resolve(context.Background(), "mixed.example"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("err=%v", err)
	}
}

func TestPolicyRejectsUnallocatedIPv6FromDNS(t *testing.T) {
	policy := Policy{Profile: config.ProfileProduction, Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("3000::1")}, nil
	})}
	if _, err := policy.Resolve(context.Background(), "reserved.example"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("err=%v", err)
	}
}

func TestPolicyRejectsMappedIPv4BeforeDNSNormalization(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		{netip.MustParseAddr("::ffff:8.8.8.8")},
		{netip.MustParseAddr("::ffff:10.0.0.1")},
		{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("::ffff:192.168.0.1")},
	} {
		policy := Policy{Profile: config.ProfileProduction, Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return addresses, nil
		})}
		if _, err := policy.Resolve(context.Background(), "mapped.example"); !errors.Is(err, ErrPolicy) {
			t.Fatalf("addresses=%v err=%v", addresses, err)
		}
	}
}
