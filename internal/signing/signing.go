// Package signing implements the interoperable WDE HMAC v1 protocol.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderVersion    = "WDE-Signature-Version"
	HeaderTimestamp  = "WDE-Timestamp"
	HeaderEventID    = "WDE-Event-ID"
	HeaderDeliveryID = "WDE-Delivery-ID"
	HeaderSignature  = "WDE-Signature"
)

var ErrInvalid = errors.New("signing: invalid signature")

func Canonical(timestamp int64, eventID, deliveryID, keyID string, body []byte) []byte {
	prefix := "v1\n" + strconv.FormatInt(timestamp, 10) + "\n" + eventID + "\n" + deliveryID + "\n" + keyID + "\n"
	result := make([]byte, 0, len(prefix)+len(body))
	result = append(result, prefix...)
	result = append(result, body...)
	return result
}
func Sign(secret []byte, timestamp int64, eventID, deliveryID, keyID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(Canonical(timestamp, eventID, deliveryID, keyID, body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func HeaderValue(keyID, signature string) string { return "key_id=" + keyID + ",v1=" + signature }

func Verify(secret []byte, now time.Time, maxSkew time.Duration, timestamp, eventID, deliveryID, keyID, signatureHeader string, body []byte) error {
	if maxSkew <= 0 || maxSkew > 15*time.Minute {
		return ErrInvalid
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalid
	}
	signedAt := time.Unix(seconds, 0)
	delta := now.Sub(signedAt)
	if delta < 0 {
		delta = -delta
	}
	if delta > maxSkew {
		return ErrInvalid
	}
	signature, ok := signatureForKey(signatureHeader, keyID)
	if !ok {
		return ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || len(decoded) != sha256.Size {
		return ErrInvalid
	}
	expected := Sign(secret, seconds, eventID, deliveryID, keyID, body)
	expectedRaw, _ := base64.RawURLEncoding.DecodeString(expected)
	if subtle.ConstantTimeCompare(decoded, expectedRaw) != 1 {
		return ErrInvalid
	}
	return nil
}

func signatureForKey(value, keyID string) (string, bool) {
	for _, entry := range strings.Split(value, ";") {
		parts := strings.Split(strings.TrimSpace(entry), ",")
		if len(parts) != 2 {
			continue
		}
		kid, ok1 := strings.CutPrefix(parts[0], "key_id=")
		sig, ok2 := strings.CutPrefix(parts[1], "v1=")
		if ok1 && ok2 && kid == keyID && kid != "" && sig != "" {
			return sig, true
		}
	}
	return "", false
}
