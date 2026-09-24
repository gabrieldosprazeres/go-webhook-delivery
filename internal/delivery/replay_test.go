package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type replayMemoryStore struct {
	commands []ReplayCommand
	err      error
}

func (s *replayMemoryStore) RequestReplay(_ context.Context, command ReplayCommand) (ReplayResult, error) {
	s.commands = append(s.commands, command)
	return ReplayResult{CommandID: command.CommandID, DeliveryID: command.DeliveryID, RunNumber: 2}, s.err
}

func TestReplayServiceBuildsVersionedOpaqueCommand(t *testing.T) {
	store := &replayMemoryStore{}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	service := NewReplayService(store, materials)
	workspaceID, deliveryID, actorID := uuid.New(), uuid.New(), uuid.New()
	result, err := service.Request(context.Background(), workspaceID, deliveryID, actorID,
		"operator-visible-key", "  recover transient partner failure  ", "req_replay")
	if err != nil {
		t.Fatal(err)
	}
	if result.DeliveryID != deliveryID || len(store.commands) != 1 {
		t.Fatalf("result=%+v commands=%d", result, len(store.commands))
	}
	command := store.commands[0]
	if command.ActorID != actorID.String() || command.Reason != "recover transient partner failure" ||
		command.FingerprintVersion != 1 || len(command.KeyHash) != 32 || len(command.Fingerprint) != 32 {
		t.Fatalf("command=%+v", command)
	}
}

func TestReplayServiceRejectsInvalidInputAndPropagatesDomainError(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := &replayMemoryStore{}
	service := NewReplayService(store, materials)
	valid := []struct {
		key, reason string
	}{{"", "reason"}, {"key", "line\nbreak"}, {"key", ""}}
	for _, test := range valid {
		if _, err := service.Request(context.Background(), uuid.New(), uuid.New(), uuid.New(),
			test.key, test.reason, "req_test"); !errors.Is(err, ErrInvalidReplay) {
			t.Fatalf("key=%q reason=%q err=%v", test.key, test.reason, err)
		}
	}
	store.err = ErrPayloadPurged
	if _, err := service.Request(context.Background(), uuid.New(), uuid.New(), uuid.New(),
		"key", "reason", "req_test"); !errors.Is(err, ErrPayloadPurged) {
		t.Fatalf("domain error=%v", err)
	}
}
