package delivery

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

type openedClaim struct {
	path, payload, secret []byte
}

func (opened openedClaim) clear() {
	clear(opened.path)
	clear(opened.payload)
	clear(opened.secret)
}

func (r *Runner) deliver(ctx context.Context, claim Claim) (bool, *int16, int, string) {
	started := r.now()
	if category := r.destinationPolicy(claim); category != "" {
		return false, nil, 0, category
	}
	opened, category := r.openClaim(claim)
	if category != "" {
		return false, nil, elapsed(started, r.now()), category
	}
	defer opened.clear()
	target, category := targetURL(claim, opened.path)
	if category != "" {
		return false, nil, elapsed(started, r.now()), category
	}
	requestCtx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()
	request, category := r.buildRequest(requestCtx, target, claim, opened)
	if category != "" {
		return false, nil, elapsed(started, r.now()), category
	}
	success, status, category := r.execute(request)
	return success, status, elapsed(started, r.now()), category
}

func (r *Runner) destinationPolicy(claim Claim) string {
	if r.profile == config.ProfileProduction {
		return "ssrf_policy_unavailable"
	}
	if claim.Scheme == "http" && !r.allowHTTP {
		return "http_destination_disabled"
	}
	if claim.Host == "localhost" {
		return ""
	}
	ip := net.ParseIP(claim.Host)
	if ip == nil || !ip.IsLoopback() {
		return "ssrf_policy"
	}
	return ""
}

func (r *Runner) openClaim(claim Claim) (openedClaim, string) {
	path, err := cryptobox.Open(r.materials.Signing, claim.Target, targetAAD(claim))
	if err != nil {
		return openedClaim{}, "decrypt_target"
	}
	payload, err := cryptobox.Open(r.materials.Payload, claim.Payload, payloadAAD(claim))
	if err != nil {
		clear(path)
		return openedClaim{}, "decrypt_payload"
	}
	secret, err := cryptobox.Open(r.materials.Signing, claim.Secret, secretAAD(claim))
	if err != nil {
		clear(path)
		clear(payload)
		return openedClaim{}, "decrypt_secret"
	}
	return openedClaim{path: path, payload: payload, secret: secret}, ""
}

func targetAAD(claim Claim) []byte {
	return cryptobox.AAD("1", claim.WorkspaceID.String(), claim.EndpointID.String(), claim.Scheme, claim.Host, strconv.Itoa(claim.Port))
}

func payloadAAD(claim Claim) []byte {
	return cryptobox.AAD("1", claim.WorkspaceID.String(), claim.EventID.String(), claim.EventType)
}

func secretAAD(claim Claim) []byte {
	return cryptobox.AAD("1", claim.WorkspaceID.String(), claim.EndpointID.String(), claim.SecretVersionID.String(), claim.KeyID)
}

func targetURL(claim Claim, path []byte) (string, string) {
	authority := claim.Host
	ip := net.ParseIP(claim.Host)
	if ip != nil && len(ip) == net.IPv6len {
		authority = "[" + claim.Host + "]"
	}
	if !defaultPort(claim.Scheme, claim.Port) {
		authority = net.JoinHostPort(claim.Host, strconv.Itoa(claim.Port))
	}
	target := claim.Scheme + "://" + authority + string(path)
	if _, err := url.ParseRequestURI(target); err != nil {
		return "", "invalid_target"
	}
	return target, ""
}

func defaultPort(scheme string, port int) bool {
	return (scheme == "http" && port == 80) || (scheme == "https" && port == 443)
}

func (r *Runner) buildRequest(ctx context.Context, target string, claim Claim, opened openedClaim) (*http.Request, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(opened.payload))
	if err != nil {
		return nil, "request_build"
	}
	timestamp := r.now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(signing.HeaderVersion, "v1")
	req.Header.Set(signing.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	req.Header.Set(signing.HeaderEventID, claim.EventID.String())
	req.Header.Set(signing.HeaderDeliveryID, claim.DeliveryID.String())
	signature := signing.Sign(opened.secret, timestamp, claim.EventID.String(), claim.DeliveryID.String(), claim.KeyID, opened.payload)
	req.Header.Set(signing.HeaderSignature, signing.HeaderValue(claim.KeyID, signature))
	return req, ""
}

func (r *Runner) execute(request *http.Request) (bool, *int16, string) {
	response, err := r.client.Do(request)
	if err != nil {
		return false, nil, "network"
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	status := int16(response.StatusCode)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return true, &status, ""
	}
	return false, &status, "http_status"
}
