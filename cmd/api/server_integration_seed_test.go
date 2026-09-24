package main

import (
	"context"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedPipelineFixture(t *testing.T, ctx context.Context, admin, super *pgxpool.Pool,
	materials cryptobox.Materials) pipelineFixture {
	t.Helper()
	fixture := pipelineFixture{victimWorkspace: uuid.New(), attackerWorkspace: uuid.New(), deliveryID: uuid.New()}
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name)
		VALUES($1,'pipeline-victim'),($2,'pipeline-attacker')`,
		fixture.victimWorkspace, fixture.attackerWorkspace); err != nil {
		t.Fatal(err)
	}
	allScopes := []string{"endpoints:write", "events:write", "deliveries:read", "deliveries:retry"}
	fixture.victimToken = insertPipelineCredential(t, ctx, admin, materials,
		fixture.victimWorkspace, allScopes)
	fixture.attackerToken = insertPipelineCredential(t, ctx, admin, materials,
		fixture.attackerWorkspace, allScopes)
	fixture.limitedToken = insertPipelineCredential(t, ctx, admin, materials,
		fixture.attackerWorkspace, []string{"deliveries:read"})
	fixture.eventOnlyToken = insertPipelineCredential(t, ctx, admin, materials,
		fixture.attackerWorkspace, []string{"events:write"})
	endpointID, firstEventID, secondEventID := uuid.New(), uuid.New(), uuid.New()
	fixture.endpointID = endpointID
	if _, err := super.Exec(ctx, `INSERT INTO wde.endpoints
		(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version)
		VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`, endpointID, fixture.victimWorkspace,
		make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.events
		(id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,
		 payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,payload_expires_at)
		VALUES($1,$2,$3,$4,1,'pipeline.test',1,$5,$6,1,2,clock_timestamp()+interval '1 day'),
		      ($7,$2,$8,$4,1,'pipeline.test',1,$5,$6,1,2,clock_timestamp()+interval '1 day')`,
		firstEventID, fixture.victimWorkspace, repeatedByte(1, 32), repeatedByte(3, 32),
		repeatedByte(4, 16), repeatedByte(5, 12), secondEventID, repeatedByte(2, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.deliveries
		(id,workspace_id,event_id,endpoint_id,status,terminal_at)
		VALUES($1,$2,$3,$4,'failed_permanent',clock_timestamp()),
		      ($5,$2,$6,$4,'pending',NULL)`, fixture.deliveryID, fixture.victimWorkspace,
		firstEventID, endpointID, uuid.New(), secondEventID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func insertPipelineCredential(t *testing.T, ctx context.Context, admin *pgxpool.Pool,
	materials cryptobox.Materials, workspaceID uuid.UUID, scopes []string) string {
	t.Helper()
	token, prefix, verifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	keyID := uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,$3,$4)`, keyID, workspaceID, prefix, verifier[:]); err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if _, err = admin.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
			VALUES($1,$2,$3)`, workspaceID, keyID, scope); err != nil {
			t.Fatal(err)
		}
	}
	return token
}

func clearPipelineBuckets(t *testing.T, ctx context.Context, super *pgxpool.Pool) {
	t.Helper()
	if _, err := super.Exec(ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
}

func repeatedByte(value byte, size int) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = value
	}
	return result
}
