package endpoint

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEndpointCursorIsTenantBoundAndTamperEvident(t *testing.T) {
	key := [32]byte{1}
	workspaceID := uuid.New()
	want := listCursor{CreatedAt: time.Now().UTC().Truncate(time.Nanosecond), ID: uuid.New()}

	encoded, err := encodeListCursor(key, workspaceID, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeListCursor(key, workspaceID, encoded)
	if err != nil || got != want {
		t.Fatalf("decoded=%#v err=%v", got, err)
	}
	if _, err = decodeListCursor(key, uuid.New(), encoded); err != ErrInvalid {
		t.Fatalf("cross-tenant cursor error=%v", err)
	}
	tampered := encoded[:len(encoded)-1] + "A"
	if tampered == encoded {
		tampered = encoded[:len(encoded)-1] + "B"
	}
	if _, err = decodeListCursor(key, workspaceID, tampered); err != ErrInvalid {
		t.Fatalf("tampered cursor error=%v", err)
	}
}
