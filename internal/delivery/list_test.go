package delivery

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOpaqueCursorRoundTripAndRejection(t *testing.T) {
	key := [32]byte{1}
	workspaceID := uuid.New()
	want := listCursor{CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 123, time.UTC), ID: uuid.New()}
	raw, err := encodeCursor(key, workspaceID, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCursor(key, workspaceID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	for _, invalid := range []string{"%%%", "e30", string(make([]byte, 257))} {
		if _, err := decodeCursor(key, workspaceID, invalid); !errors.Is(err, ErrInvalidList) {
			t.Fatalf("cursor=%q err=%v", invalid, err)
		}
	}
	if _, err := decodeCursor(key, uuid.New(), raw); !errors.Is(err, ErrInvalidList) {
		t.Fatalf("cross-tenant cursor err=%v", err)
	}
	tampered := []byte(raw)
	tampered[len(tampered)-1] ^= 1
	if _, err := decodeCursor(key, workspaceID, string(tampered)); !errors.Is(err, ErrInvalidList) {
		t.Fatalf("tampered cursor err=%v", err)
	}
}
