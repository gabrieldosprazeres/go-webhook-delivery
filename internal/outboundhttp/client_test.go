package outboundhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

func TestDialerPinsValidatedAddressAndRevalidatesNewConnection(t *testing.T) {
	var resolutions atomic.Int32
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		if resolutions.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	var dialed string
	dialer := Dialer{Policy: Policy{Profile: config.ProfileProduction, Resolver: resolver},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			left, right := net.Pipe()
			_ = right.Close()
			return left, nil
		}}
	conn, err := dialer.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if dialed != "93.184.216.34:443" || strings.Contains(dialed, "example.com") {
		t.Fatalf("dialed=%q", dialed)
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "example.com:443"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("rebinding err=%v", err)
	}
}

func TestClientIgnoresProxyAndRejectsRedirectAndInvalidTLS(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	client := NewClient(config.ProfileTest)
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil || os.Getenv("HTTPS_PROXY") == "" {
		t.Fatal("environment proxy was not explicitly disabled")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := client.Get(redirect.URL); !errors.Is(err, ErrRedirect) {
		t.Fatalf("redirect err=%v", err)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsServer.Close()
	if _, err := client.Get(tlsServer.URL); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}

func TestClientHonorsCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	if _, err := NewClient(config.ProfileTest).Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow response err=%v", err)
	}
}

func TestClientPreservesHostnameForTLSWhileDialingPinnedIP(t *testing.T) {
	var receivedSNI string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil || len(certificate.DNSNames) == 0 {
		t.Fatalf("certificate=%v err=%v", certificate, err)
	}
	hostname := certificate.DNSNames[0]
	server.TLS.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		receivedSNI = hello.ServerName
		return nil, nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	client := NewClient(config.ProfileProduction)
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = roots
	var pinnedAddress string
	transport.DialContext = Dialer{
		Policy: Policy{Profile: config.ProfileProduction, Resolver: resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		})},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			pinnedAddress = address
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}.DialContext
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	response, err := client.Get("https://" + net.JoinHostPort(hostname, port))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if receivedSNI != hostname || !strings.HasPrefix(pinnedAddress, "93.184.216.34:") {
		t.Fatalf("sni=%q pinned=%q", receivedSNI, pinnedAddress)
	}
}

func TestClientRejectsResponseHeadersOverLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Oversized", strings.Repeat("x", 64<<10))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if _, err := NewClient(config.ProfileTest).Get(server.URL); err == nil {
		t.Fatal("oversized response headers accepted")
	}
}

func TestDialerSharesCallerDeadlineAcrossMultipleAddresses(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("2606:4700:4700::1111"),
			netip.MustParseAddr("8.8.8.8"),
		}, nil
	})
	var attempts atomic.Int32
	dialer := Dialer{Policy: Policy{Profile: config.ProfileProduction, Resolver: resolver},
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			attempts.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := dialer.DialContext(ctx, "tcp", "slow.example:443")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 100*time.Millisecond || attempts.Load() != 3 {
		t.Fatalf("err=%v elapsed=%s attempts=%d", err, time.Since(started), attempts.Load())
	}
}
