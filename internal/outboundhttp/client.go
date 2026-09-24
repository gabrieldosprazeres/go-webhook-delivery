package outboundhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

var ErrRedirect = errors.New("outboundhttp: redirect disabled")

type Dialer struct {
	Policy Policy
	Dial   func(context.Context, string, string) (net.Conn, error)
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrPolicy
	}
	addresses, err := d.Policy.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	dial := d.Dial
	if dial == nil {
		netDialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
		dial = netDialer.DialContext
	}
	var last error
	for _, ip := range addresses {
		conn, dialErr := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		last = dialErr
	}
	return nil, last
}

func NewClient(profile config.Profile) *http.Client {
	dialer := Dialer{Policy: Policy{Profile: profile}}
	transport := &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
		ForceAttemptHTTP2:      true,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return ErrRedirect
	}}
}

func Authority(host string, port int, scheme string) string {
	if (scheme == "https" && port == 443) || (scheme == "http" && port == 80) {
		if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
