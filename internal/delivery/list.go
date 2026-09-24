package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/google/uuid"
)

type listCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

const (
	cursorVersion     = byte(1)
	cursorPayloadSize = 1 + 16 + 8 + 16
	cursorMACSize     = sha256.Size
)

func encodeCursor(key [32]byte, workspaceID uuid.UUID, value listCursor) (string, error) {
	if key == ([32]byte{}) || workspaceID == uuid.Nil || value.ID == uuid.Nil || value.CreatedAt.IsZero() {
		return "", ErrInvalidList
	}
	payload := make([]byte, cursorPayloadSize, cursorPayloadSize+cursorMACSize)
	payload[0] = cursorVersion
	copy(payload[1:17], workspaceID[:])
	binary.BigEndian.PutUint64(payload[17:25], uint64(value.CreatedAt.UnixNano()))
	copy(payload[25:41], value.ID[:])
	mac := cursorMAC(key, payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac[:]...)), nil
}

func decodeCursor(key [32]byte, workspaceID uuid.UUID, raw string) (listCursor, error) {
	if raw == "" {
		return listCursor{}, nil
	}
	if key == ([32]byte{}) || workspaceID == uuid.Nil || len(raw) > 256 {
		return listCursor{}, ErrInvalidList
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != cursorPayloadSize+cursorMACSize || decoded[0] != cursorVersion {
		return listCursor{}, ErrInvalidList
	}
	payload, suppliedMAC := decoded[:cursorPayloadSize], decoded[cursorPayloadSize:]
	expectedMAC := cursorMAC(key, payload)
	if !hmac.Equal(suppliedMAC, expectedMAC[:]) || !hmac.Equal(payload[1:17], workspaceID[:]) {
		return listCursor{}, ErrInvalidList
	}
	value := listCursor{CreatedAt: time.Unix(0, int64(binary.BigEndian.Uint64(payload[17:25]))).UTC()}
	copy(value.ID[:], payload[25:41])
	if value.ID == uuid.Nil || value.CreatedAt.IsZero() {
		return listCursor{}, ErrInvalidList
	}
	return value, nil
}

func cursorMAC(key [32]byte, payload []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("delivery-cursor:v1\n"))
	_, _ = mac.Write(payload)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
