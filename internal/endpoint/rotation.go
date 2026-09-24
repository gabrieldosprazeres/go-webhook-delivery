package endpoint

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

const (
	defaultRotationOverlap = 24 * 60 * 60
	maxRotationOverlap     = 7 * 24 * 60 * 60
)

func (s *Service) Rotate(ctx context.Context, workspaceID, endpointID uuid.UUID, actorID, requestID,
	idempotencyKey string, input RotationInput) (RotationResult, error) {
	if len(idempotencyKey) < 1 || len(idempotencyKey) > 128 {
		return RotationResult{}, ErrInvalid
	}
	overlap := input.OverlapSeconds
	if overlap == 0 {
		overlap = defaultRotationOverlap
	}
	if overlap < 3600 || overlap > maxRotationOverlap {
		return RotationResult{}, ErrInvalid
	}
	commandID, auditID, secretID, err := rotationIDs()
	if err != nil {
		return RotationResult{}, err
	}
	secret, keyID, err := newSigningSecret()
	if err != nil {
		return RotationResult{}, err
	}
	defer clear(secret)
	envelope, err := cryptobox.Seal(s.materials.Signing, secret,
		secretAssociatedData(cryptobox.FormatVersion, workspaceID, endpointID, secretID, keyID))
	if err != nil {
		return RotationResult{}, err
	}
	record := RotationRecord{
		CommandID: commandID, AuditID: auditID, WorkspaceID: workspaceID, EndpointID: endpointID,
		SecretVersionID: secretID, KeyID: keyID, ActorID: actorID, RequestID: requestID,
		IdempotencyHash: rotationMAC(s.materials.IdempotencyPepper, []byte("secret-rotation:v1\n"+idempotencyKey)),
		Fingerprint:     rotationFingerprint(s.materials.FingerprintPepper, endpointID, overlap),
		OverlapSeconds:  overlap, Secret: envelope,
	}
	stored, err := s.store.Rotate(ctx, record)
	if err != nil {
		return RotationResult{}, err
	}
	plain, err := cryptobox.Open(s.materials.Signing, stored.Secret,
		secretAssociatedData(stored.Secret.FormatVersion, workspaceID, endpointID, stored.SecretVersionID, stored.KeyID))
	if err != nil {
		return RotationResult{}, err
	}
	defer clear(plain)
	return RotationResult{EndpointID: endpointID,
		Secret:    SigningSecret{KeyID: stored.KeyID, Secret: base64.RawURLEncoding.EncodeToString(plain)},
		Duplicate: stored.Duplicate}, nil
}

func rotationIDs() (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	ids := [3]uuid.UUID{}
	for index := range ids {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, uuid.Nil, uuid.Nil, err
		}
		ids[index] = id
	}
	return ids[0], ids[1], ids[2], nil
}

func rotationFingerprint(key [32]byte, endpointID uuid.UUID, overlap int) []byte {
	value := make([]byte, 0, 16+8)
	value = append(value, endpointID[:]...)
	var seconds [8]byte
	binary.BigEndian.PutUint64(seconds[:], uint64(overlap))
	value = append(value, seconds[:]...)
	return rotationMAC(key, value)
}

func rotationMAC(key [32]byte, value []byte) []byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}
