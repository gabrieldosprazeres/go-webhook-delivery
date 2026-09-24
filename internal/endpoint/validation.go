package endpoint

import (
	"fmt"
	"regexp"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/outboundhttp"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

var eventTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func validateURL(profile config.Profile, allowHTTP bool, raw string) (string, string, int, string, error) {
	destination, err := outboundhttp.Parse(profile, allowHTTP, raw)
	if err != nil {
		return "", "", 0, "", ErrInvalid
	}
	return destination.Scheme, destination.Host, destination.Port, destination.Path, nil
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
	return fmt.Sprintf("%s://%s%s", scheme, outboundhttp.Authority(host, port, scheme), path)
}
