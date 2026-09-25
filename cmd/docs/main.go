package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

const (
	defaultDocsAddress = ":8080"
	defaultStaticDir   = "/static"
	localServerLine    = "  - url: http://localhost:8080"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("docs: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	address := environment("WDE_DOCS_HTTP_ADDR", defaultDocsAddress)
	if len(args) == 1 && args[0] == "healthcheck" {
		return operational.CheckLive(parent, healthcheckAddress(address))
	}
	if len(args) != 0 {
		return errors.New("docs: invalid arguments")
	}
	apiURL, origin, err := validateAPIURL(environment("WDE_DOCS_API_URL", ""))
	if err != nil {
		return err
	}
	staticDir := environment("WDE_DOCS_STATIC_DIR", defaultStaticDir)
	spec, err := loadProductionSpec(staticDir+"/openapi.yaml", apiURL)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           routes(staticDir, spec, origin),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	ctx, stop := appruntime.SignalContext(parent)
	defer stop()
	return appruntime.Serve(ctx, server, listener, 10*time.Second)
}

func validateAPIURL(raw string) (string, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !validDNSName(parsed.Hostname()) {
		return "", "", errors.New("docs: WDE_DOCS_API_URL must be an HTTPS origin")
	}
	if port := parsed.Port(); port != "" {
		value, portErr := strconv.Atoi(port)
		if portErr != nil || value < 1 || value > 65535 {
			return "", "", errors.New("docs: WDE_DOCS_API_URL must be an HTTPS origin")
		}
	}
	origin := parsed.Scheme + "://" + parsed.Host
	return origin, origin, nil
}

func validDNSName(host string) bool {
	if host == "" || len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func loadProductionSpec(path, apiURL string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("docs: OpenAPI contract cannot be read")
	}
	if bytes.Count(contents, []byte(localServerLine)) != 1 {
		return nil, errors.New("docs: OpenAPI contract has an unexpected servers block")
	}
	return bytes.Replace(contents, []byte(localServerLine), []byte("  - url: "+apiURL), 1), nil
}

func routes(staticDir string, spec []byte, apiOrigin string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /openapi.yaml", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = response.Write(spec)
	})
	mux.Handle("GET /", http.FileServer(http.Dir(staticDir)))
	return securityHeaders(mux, apiOrigin)
}

func securityHeaders(next http.Handler, apiOrigin string) http.Handler {
	policy := strings.Join([]string{
		"default-src 'self'", "script-src 'self'", "style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:", "font-src 'self' data:", "connect-src 'self' " + apiOrigin,
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'none'",
	}, "; ")
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", policy)
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func healthcheckAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
