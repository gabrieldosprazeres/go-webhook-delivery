// Package outboundhttp owns destination parsing and network egress policy.
package outboundhttp

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"golang.org/x/net/idna"
)

var ErrPolicy = errors.New("outboundhttp: destination rejected")

type Destination struct {
	Scheme, Host, Path string
	Port               int
}

func Parse(profile config.Profile, allowHTTP bool, raw string) (Destination, error) {
	if raw == "" || strings.Contains(raw, "#") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return Destination{}, ErrPolicy
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil ||
		parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return Destination{}, ErrPolicy
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && profile != config.ProfileProduction && allowHTTP) {
		return Destination{}, ErrPolicy
	}
	host, err := canonicalHost(parsed.Hostname())
	if err != nil {
		return Destination{}, err
	}
	port := 443
	if scheme == "http" {
		port = 80
	}
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return Destination{}, ErrPolicy
		}
	}
	if scheme == "http" && !isLoopbackLiteralOrName(host) {
		return Destination{}, ErrPolicy
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return Destination{Scheme: scheme, Host: host, Port: port, Path: path}, nil
}

func canonicalHost(raw string) (string, error) {
	if strings.Contains(raw, "%") || strings.HasPrefix(raw, ".") {
		return "", ErrPolicy
	}
	value := strings.ToLower(strings.TrimSuffix(raw, "."))
	if value == "" || len(value) > 253 {
		return "", ErrPolicy
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		if addr.Zone() != "" || addr.Is4In6() {
			return "", ErrPolicy
		}
		return addr.String(), nil
	}
	if strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && r != '.' }) == -1 {
		return "", ErrPolicy
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || ascii == "" || len(ascii) > 253 || net.ParseIP(ascii) != nil {
		return "", ErrPolicy
	}
	return strings.ToLower(ascii), nil
}

func isLoopbackLiteralOrName(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}
