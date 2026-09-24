package endpoint

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

var eventTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func validateURL(profile config.Profile, allowHTTP bool, raw string) (string, string, int, string, error) {
	if profile == config.ProfileProduction {
		return "", "", 0, "", ErrUnavailable
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Fragment != "" || parsed.Hostname() == "" {
		return "", "", 0, "", ErrInvalid
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", 0, "", ErrInvalid
	}
	if scheme == "http" && !allowHTTP {
		return "", "", 0, "", ErrInvalid
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !isLoopbackHost(host) {
		return "", "", 0, "", ErrInvalid
	}
	port, err := destinationPort(parsed, scheme)
	if err != nil {
		return "", "", 0, "", err
	}
	return scheme, host, port, destinationPath(parsed), nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func destinationPort(parsed *url.URL, scheme string) (int, error) {
	port := 80
	if scheme == "https" {
		port = 443
	}
	if parsed.Port() == "" {
		return port, nil
	}
	value, err := strconv.Atoi(parsed.Port())
	if err != nil || value < 1 || value > 65535 {
		return 0, ErrInvalid
	}
	return value, nil
}

func destinationPath(parsed *url.URL) string {
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return path
}

func validateEventTypes(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 100 {
		return nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) > 128 || !eventTypePattern.MatchString(value) {
			return nil, ErrInvalid
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, ErrInvalid
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func buildURL(scheme, host string, port int, path string) string {
	defaultPort := (scheme == "http" && port == 80) || (scheme == "https" && port == 443)
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if !defaultPort {
		authority = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return fmt.Sprintf("%s://%s%s", scheme, authority, path)
}
