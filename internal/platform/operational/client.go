package operational

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// CheckLive is used by the distroless container healthcheck and only dials loopback.
func CheckLive(ctx context.Context, address string) error {
	return check(ctx, address, "/livez")
}

// CheckReady verifies the dependency-aware probe on loopback.
func CheckReady(ctx context.Context, address string) error {
	return check(ctx, address, "/readyz")
}

func check(ctx context.Context, address, path string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("operational: invalid address")
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", port), Path: path}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
		ResponseHeaderTimeout: time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("operational: redirect denied")
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("operational: liveness unavailable")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 256))
	if response.StatusCode != http.StatusOK {
		return errors.New("operational: liveness unavailable")
	}
	return nil
}
