// Package endpoint manages webhook destinations and their signing secrets.
package endpoint

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strconv"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/outboundhttp"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type Service struct {
	store     Store
	profile   config.Profile
	allowHTTP bool
	materials cryptobox.Materials
	policy    outboundhttp.Policy
}

func NewService(store Store, profile config.Profile, allowHTTP bool, materials cryptobox.Materials) *Service {
	return &Service{
		store: store, profile: profile, allowHTTP: allowHTTP, materials: materials,
		policy: outboundhttp.Policy{Profile: profile},
	}
}

func (s *Service) Create(ctx context.Context, workspaceID uuid.UUID, input CreateInput) (Created, error) {
	return s.CreateAs(ctx, workspaceID, "system", "internal", "internal", input)
}

func (s *Service) CreateAs(ctx context.Context, workspaceID uuid.UUID, actorType, actorID, requestID string, input CreateInput) (Created, error) {
	destination, eventTypes, err := s.validateCreateInput(ctx, input)
	if err != nil {
		return Created{}, err
	}
	record, secret, err := s.newRecord(workspaceID, actorType, actorID, requestID, destination, eventTypes)
	if err != nil {
		return Created{}, err
	}
	if err := s.store.Create(ctx, record); err != nil {
		return Created{}, err
	}
	return createdResponse(record, destination, secret), nil
}

func (s *Service) validateCreateInput(ctx context.Context, input CreateInput) (destination, []string, error) {
	scheme, host, port, path, err := validateURL(s.profile, s.allowHTTP, input.URL)
	if err != nil {
		return destination{}, nil, err
	}
	if _, err := s.policy.Resolve(ctx, host); err != nil {
		return destination{}, nil, ErrInvalid
	}
	eventTypes, err := validateEventTypes(input.EventTypes)
	return destination{scheme: scheme, host: host, port: port, path: path}, eventTypes, err
}

func (s *Service) newRecord(workspaceID uuid.UUID, actorType, actorID, requestID string, target destination, eventTypes []string) (NewRecord, string, error) {
	id, secretID, err := newEndpointIDs()
	if err != nil {
		return NewRecord{}, "", err
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return NewRecord{}, "", err
	}
	secretRaw, keyID, err := newSigningSecret()
	if err != nil {
		return NewRecord{}, "", err
	}
	defer clear(secretRaw)
	targetEnvelope, secretEnvelope, err := s.sealRecord(workspaceID, id, secretID, keyID, target, secretRaw)
	if err != nil {
		return NewRecord{}, "", err
	}
	record := NewRecord{
		ID: id, WorkspaceID: workspaceID, SecretVersionID: secretID, AuditID: auditID,
		Status: "active", Scheme: target.scheme, Host: target.host, Port: target.port,
		KeyID: keyID, ActorType: actorType, ActorID: actorID, RequestID: requestID,
		Target: targetEnvelope, Secret: secretEnvelope, EventTypes: eventTypes,
	}
	return record, base64.RawURLEncoding.EncodeToString(secretRaw), nil
}

func newEndpointIDs() (uuid.UUID, uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	secretID, err := uuid.NewV7()
	return id, secretID, err
}

func newSigningSecret() ([]byte, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", err
	}
	keyRaw := make([]byte, 8)
	if _, err := rand.Read(keyRaw); err != nil {
		clear(secret)
		return nil, "", err
	}
	keyID := "key_" + base64.RawURLEncoding.EncodeToString(keyRaw)
	clear(keyRaw)
	return secret, keyID, nil
}

func (s *Service) sealRecord(workspaceID, id, secretID uuid.UUID, keyID string, target destination, secret []byte) (cryptobox.Envelope, cryptobox.Envelope, error) {
	targetAAD := targetAssociatedData(cryptobox.FormatVersion, workspaceID, id, target)
	targetEnvelope, err := cryptobox.Seal(s.materials.Signing, []byte(target.path), targetAAD)
	if err != nil {
		return cryptobox.Envelope{}, cryptobox.Envelope{}, err
	}
	secretAAD := secretAssociatedData(cryptobox.FormatVersion, workspaceID, id, secretID, keyID)
	secretEnvelope, err := cryptobox.Seal(s.materials.Signing, secret, secretAAD)
	return targetEnvelope, secretEnvelope, err
}

func createdResponse(record NewRecord, target destination, secret string) Created {
	return Created{
		Endpoint:      Endpoint{ID: record.ID, URL: buildURL(target.scheme, target.host, target.port, target.path), Status: record.Status, EventTypes: record.EventTypes},
		SigningSecret: SigningSecret{KeyID: record.KeyID, Secret: secret},
	}
}

func (s *Service) Get(ctx context.Context, workspaceID, id uuid.UUID) (Endpoint, error) {
	record, err := s.store.Get(ctx, workspaceID, id)
	if err != nil {
		return Endpoint{}, err
	}
	aad := targetAssociatedData(record.Target.FormatVersion, workspaceID, id,
		destination{scheme: record.Scheme, host: record.Host, port: record.Port})
	path, err := cryptobox.Open(s.materials.Signing, record.Target, aad)
	if err != nil {
		return Endpoint{}, err
	}
	defer clear(path)
	return Endpoint{ID: id, URL: buildURL(record.Scheme, record.Host, record.Port, string(path)), Status: record.Status, EventTypes: record.EventTypes}, nil
}

func targetAssociatedData(version int16, workspaceID, endpointID uuid.UUID, target destination) []byte {
	if version == cryptobox.LegacyFormatVersion {
		return cryptobox.AAD("1", workspaceID.String(), endpointID.String(), target.scheme, target.host, strconv.Itoa(target.port))
	}
	return cryptobox.AAD("2", workspaceID.String(), "endpoint_target", endpointID.String(), target.scheme, target.host, strconv.Itoa(target.port))
}

func secretAssociatedData(version int16, workspaceID, endpointID, secretID uuid.UUID, keyID string) []byte {
	if version == cryptobox.LegacyFormatVersion {
		return cryptobox.AAD("1", workspaceID.String(), endpointID.String(), secretID.String(), keyID)
	}
	return cryptobox.AAD("2", workspaceID.String(), "signing_secret", endpointID.String(), secretID.String(), keyID)
}
