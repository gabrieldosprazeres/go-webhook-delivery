// Package event owns idempotent event ingestion and transactional fan-out.
package event

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

const MaxPayloadBytes = 1 << 20

var (
	ErrInvalid       = errors.New("event: invalid")
	ErrConflict      = errors.New("event: idempotency conflict")
	eventTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

type PublishResult struct {
	EventID     uuid.UUID   `json:"event_id"`
	DeliveryIDs []uuid.UUID `json:"delivery_ids"`
	Duplicate   bool        `json:"duplicate"`
}
type NewRecord struct {
	ID, WorkspaceID      uuid.UUID
	EventType            string
	KeyHash, Fingerprint []byte
	FingerprintVersion   int16
	Payload              cryptobox.Envelope
	PayloadSize          int
	PayloadExpiresAt     time.Time
}
type StoreResult struct {
	EventID     uuid.UUID
	DeliveryIDs []uuid.UUID
	Duplicate   bool
}
type Store interface {
	Publish(context.Context, NewRecord) (StoreResult, error)
}
type Service struct {
	store     Store
	materials cryptobox.Materials
}

func NewService(store Store, materials cryptobox.Materials) *Service {
	return &Service{store: store, materials: materials}
}

func (s *Service) Publish(ctx context.Context, workspaceID uuid.UUID, idempotencyKey string, raw []byte) (PublishResult, error) {
	if len(idempotencyKey) < 1 || len(idempotencyKey) > 128 || len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return PublishResult{}, ErrInvalid
	}
	eventType, canonical, err := validateAndCanonicalize(raw)
	if err != nil {
		return PublishResult{}, ErrInvalid
	}
	id, err := uuid.NewV7()
	if err != nil {
		return PublishResult{}, err
	}
	keyHash := keyed(s.materials.IdempotencyPepper, []byte("idempotency:v1\n"+idempotencyKey))
	fingerprintInput := make([]byte, 0, len(canonical)+len(eventType)+8)
	fingerprintInput = append(fingerprintInput, "v1\n"...)
	fingerprintInput = append(fingerprintInput, eventType...)
	fingerprintInput = append(fingerprintInput, "\nall\n"...)
	fingerprintInput = append(fingerprintInput, canonical...)
	fingerprint := keyed(s.materials.FingerprintPepper, fingerprintInput)
	clear(fingerprintInput)
	clear(canonical)
	aad := cryptobox.AAD("2", workspaceID.String(), "event_payload", id.String(), eventType)
	envelope, err := cryptobox.Seal(s.materials.Payload, raw, aad)
	if err != nil {
		return PublishResult{}, err
	}
	record := NewRecord{ID: id, WorkspaceID: workspaceID, EventType: eventType, KeyHash: keyHash[:], Fingerprint: fingerprint[:], FingerprintVersion: 1, Payload: envelope, PayloadSize: len(raw), PayloadExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}
	stored, err := s.store.Publish(ctx, record)
	if err != nil {
		return PublishResult{}, err
	}
	return PublishResult(stored), nil
}

func validateAndCanonicalize(raw []byte) (string, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoded, err := decodeUnique(decoder)
	if err != nil {
		return "", nil, err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return "", nil, ErrInvalid
	}
	value, ok := decoded.(map[string]any)
	if !ok {
		return "", nil, ErrInvalid
	}
	eventType, ok := value["type"].(string)
	if !ok || len(eventType) > 128 || !eventTypePattern.MatchString(eventType) {
		return "", nil, ErrInvalid
	}
	if _, ok := value["data"]; !ok {
		return "", nil, ErrInvalid
	}
	canonical, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return "", nil, err
	}
	return eventType, canonical, nil
}

func decodeUnique(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		value := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, duplicate := value[key]; duplicate {
				return nil, ErrInvalid
			}
			child, err := decodeUnique(decoder)
			if err != nil {
				return nil, err
			}
			value[key] = child
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, ErrInvalid
		}
		return value, nil
	case '[':
		var value []any
		for decoder.More() {
			child, err := decodeUnique(decoder)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, ErrInvalid
		}
		return value, nil
	default:
		return nil, ErrInvalid
	}
}

func keyed(key [32]byte, value []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(value)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
