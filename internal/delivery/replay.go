package delivery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type ReplayService struct {
	store     ReplayStore
	materials cryptobox.Materials
}

func NewReplayService(store ReplayStore, materials cryptobox.Materials) *ReplayService {
	return &ReplayService{store: store, materials: materials}
}

func (s *ReplayService) Request(ctx context.Context, workspaceID, deliveryID, actorID uuid.UUID,
	idempotencyKey, reason, requestID string) (ReplayResult, error) {
	reason = strings.TrimSpace(reason)
	if workspaceID == uuid.Nil || deliveryID == uuid.Nil || actorID == uuid.Nil ||
		len(idempotencyKey) < 1 || len(idempotencyKey) > 128 || !validReason(reason) ||
		len(requestID) < 1 || len(requestID) > 128 {
		return ReplayResult{}, ErrInvalidReplay
	}
	commandID, err := uuid.NewV7()
	if err != nil {
		return ReplayResult{}, err
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return ReplayResult{}, err
	}
	keyHash := replayMAC(s.materials.IdempotencyPepper, "replay-key:v1\n"+idempotencyKey)
	fingerprint := replayMAC(s.materials.FingerprintPepper,
		"replay-fingerprint:v1\n"+deliveryID.String()+"\n"+reason)
	return s.store.RequestReplay(ctx, ReplayCommand{
		CommandID: commandID, AuditID: auditID, WorkspaceID: workspaceID, DeliveryID: deliveryID,
		ActorID: actorID.String(), Reason: reason, RequestID: requestID,
		KeyHash: keyHash[:], Fingerprint: fingerprint[:], FingerprintVersion: 1,
	})
}

func validReason(value string) bool {
	if len(value) < 1 || len(value) > 500 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func replayMAC(key [32]byte, value string) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(value))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
