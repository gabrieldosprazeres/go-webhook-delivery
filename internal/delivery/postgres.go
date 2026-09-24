package delivery

import (
	"context"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultClaimTimeout = 2 * time.Second

type PostgresStore struct {
	pool         *pgxpool.Pool
	claimTimeout time.Duration
	cursorPepper [32]byte
}

func NewPostgresStore(pool *pgxpool.Pool, cursorPepper ...[32]byte) *PostgresStore {
	store := &PostgresStore{pool: pool, claimTimeout: defaultClaimTimeout}
	if len(cursorPepper) == 1 {
		store.cursorPepper = cursorPepper[0]
	}
	return store
}

func (s *PostgresStore) ClaimBatch(ctx context.Context, request ClaimRequest) ([]Claim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	claimCtx, cancel := context.WithTimeout(ctx, s.claimTimeout)
	defer cancel()
	attemptIDs := make([]uuid.UUID, request.Limit)
	for index := range attemptIDs {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		attemptIDs[index] = id
	}
	rows, err := s.pool.Query(claimCtx, `SELECT workspace_id,delivery_id,event_id,endpoint_id,
		fencing_token,attempt_number,max_attempts,scheme,host_ascii,port,
		target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version,
		event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,
		key_id,secret_version_id,secret_cipher_format_version,secret_ciphertext,secret_nonce,secret_kek_version,
		retiring_key_id,retiring_secret_version_id,retiring_cipher_format_version,
		retiring_secret_ciphertext,retiring_secret_nonce,retiring_kek_version
		FROM wde.claim_deliveries($1,$2,$3,$4,$5,$6)`, request.WorkerID, attemptIDs,
		request.LeaseTTL, request.Limit, request.WorkspaceLimit, request.EndpointLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]Claim, 0, request.Limit)
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func scanClaim(row pgx.Row) (Claim, error) {
	var c Claim
	var retiringKeyID *string
	var retiringID *uuid.UUID
	var retiringFormat, retiringKEK *int16
	var retiringCiphertext, retiringNonce []byte
	err := row.Scan(&c.WorkspaceID, &c.DeliveryID, &c.EventID, &c.EndpointID,
		&c.FencingToken, &c.AttemptNumber, &c.MaxAttempts, &c.Scheme, &c.Host, &c.Port,
		&c.Target.FormatVersion, &c.Target.Ciphertext, &c.Target.Nonce, &c.Target.KEKVersion,
		&c.EventType, &c.Payload.FormatVersion, &c.Payload.Ciphertext, &c.Payload.Nonce,
		&c.Payload.KEKVersion, &c.KeyID, &c.SecretVersionID, &c.Secret.FormatVersion,
		&c.Secret.Ciphertext, &c.Secret.Nonce, &c.Secret.KEKVersion,
		&retiringKeyID, &retiringID, &retiringFormat, &retiringCiphertext, &retiringNonce, &retiringKEK)
	if err == nil && retiringKeyID != nil && retiringID != nil && retiringFormat != nil && retiringKEK != nil {
		c.Retiring = &ClaimSecret{KeyID: *retiringKeyID, VersionID: *retiringID,
			Envelope: cryptobox.Envelope{FormatVersion: *retiringFormat, KEKVersion: *retiringKEK,
				Ciphertext: retiringCiphertext, Nonce: retiringNonce}}
	}
	return c, err
}

func (s *PostgresStore) Finalize(ctx context.Context, c Claim, workerID uuid.UUID, result Result) (bool, error) {
	result = normalizeResult(result)
	var changed bool
	var retryDelay any
	if result.Disposition == DispositionRetry {
		retryDelay = result.RetryAfter
	}
	err := s.pool.QueryRow(ctx, `SELECT wde.finalize_delivery($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.WorkspaceID, c.DeliveryID, workerID, c.FencingToken, result.Disposition,
		result.HTTPStatus, result.DurationMS, result.Category, retryDelay).Scan(&changed)
	return changed, err
}

func normalizeResult(result Result) Result {
	if result.HTTPStatus == nil || (*result.HTTPStatus >= 100 && *result.HTTPStatus <= 599) {
		return result
	}
	result.HTTPStatus = nil
	result.Disposition = DispositionPermanent
	result.Category = "invalid_http_status"
	result.RetryAfter = 0
	return result
}
