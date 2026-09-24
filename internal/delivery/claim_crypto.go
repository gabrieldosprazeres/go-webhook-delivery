package delivery

import (
	"strconv"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type openedClaim struct {
	path, payload, secret, retiring []byte
}

func (opened openedClaim) clear() {
	clear(opened.path)
	clear(opened.payload)
	clear(opened.secret)
	clear(opened.retiring)
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
	opened := openedClaim{path: path, payload: payload, secret: secret}
	if claim.Retiring != nil {
		opened.retiring, err = cryptobox.Open(r.materials.Signing, claim.Retiring.Envelope,
			secretEnvelopeAAD(claim.Retiring.Envelope.FormatVersion, claim.WorkspaceID,
				claim.EndpointID, claim.Retiring.VersionID, claim.Retiring.KeyID))
		if err != nil {
			opened.clear()
			return openedClaim{}, "decrypt_secret"
		}
	}
	return opened, ""
}

func targetAAD(claim Claim) []byte {
	if claim.Target.FormatVersion == cryptobox.LegacyFormatVersion {
		return cryptobox.AAD("1", claim.WorkspaceID.String(), claim.EndpointID.String(), claim.Scheme, claim.Host, strconv.Itoa(claim.Port))
	}
	return cryptobox.AAD("2", claim.WorkspaceID.String(), "endpoint_target", claim.EndpointID.String(), claim.Scheme, claim.Host, strconv.Itoa(claim.Port))
}

func payloadAAD(claim Claim) []byte {
	if claim.Payload.FormatVersion == cryptobox.LegacyFormatVersion {
		return cryptobox.AAD("1", claim.WorkspaceID.String(), claim.EventID.String(), claim.EventType)
	}
	return cryptobox.AAD("2", claim.WorkspaceID.String(), "event_payload", claim.EventID.String(), claim.EventType)
}

func secretAAD(claim Claim) []byte {
	return secretEnvelopeAAD(claim.Secret.FormatVersion, claim.WorkspaceID, claim.EndpointID, claim.SecretVersionID, claim.KeyID)
}

func secretEnvelopeAAD(version int16, workspaceID, endpointID, secretID uuid.UUID, keyID string) []byte {
	if version == cryptobox.LegacyFormatVersion {
		return cryptobox.AAD("1", workspaceID.String(), endpointID.String(), secretID.String(), keyID)
	}
	return cryptobox.AAD("2", workspaceID.String(), "signing_secret", endpointID.String(), secretID.String(), keyID)
}
