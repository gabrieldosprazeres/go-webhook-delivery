package ratelimit

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDeliveryDimensionDigestIsTenantBoundAndVersioned(t *testing.T) {
	pepper := [32]byte{1}
	resourceID := uuid.New()
	policy := Policy{Operation: "replay", Window: time.Minute}
	first := dimension{typeName: "delivery", workspaceID: uuid.New(), value: resourceID}
	second := dimension{typeName: "delivery", workspaceID: uuid.New(), value: resourceID}
	if bytes.Equal(dimensionDigest(pepper, policy, first), dimensionDigest(pepper, policy, second)) {
		t.Fatal("same resource UUID collided across workspaces")
	}
	changedWindow := policy
	changedWindow.Window = 2 * time.Minute
	if bytes.Equal(dimensionDigest(pepper, policy, first), dimensionDigest(pepper, changedWindow, first)) {
		t.Fatal("window metadata is absent from the digest")
	}
}
