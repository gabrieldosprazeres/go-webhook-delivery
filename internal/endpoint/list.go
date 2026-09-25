package endpoint

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type ListItem struct {
	ID         uuid.UUID `json:"id"`
	URL        string    `json:"url"`
	Status     string    `json:"status"`
	EventTypes []string  `json:"event_types"`
	CreatedAt  time.Time `json:"created_at"`
}

type ListResult struct {
	Items      []ListItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type ListRecord struct {
	ID, WorkspaceID      uuid.UUID
	Status, Scheme, Host string
	Port                 int
	Target               cryptobox.Envelope
	EventTypes           []string
	CreatedAt            time.Time
}

type ListStore interface {
	ListEndpoints(context.Context, uuid.UUID, int, listCursor) ([]ListRecord, error)
}

type Lister struct {
	store        ListStore
	materials    cryptobox.Materials
	cursorPepper [32]byte
}

func NewLister(store ListStore, materials cryptobox.Materials) *Lister {
	return &Lister{store: store, materials: materials, cursorPepper: materials.CursorPepper}
}

func (l *Lister) List(ctx context.Context, workspaceID uuid.UUID, limit int, rawCursor string) (ListResult, error) {
	if workspaceID == uuid.Nil || limit < 1 || limit > 100 {
		return ListResult{}, ErrInvalid
	}
	cursor, err := decodeListCursor(l.cursorPepper, workspaceID, rawCursor)
	if err != nil {
		return ListResult{}, err
	}
	records, err := l.store.ListEndpoints(ctx, workspaceID, limit+1, cursor)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: make([]ListItem, 0, min(limit, len(records)))}
	for index, record := range records {
		if index == limit {
			last := records[index-1]
			result.NextCursor, err = encodeListCursor(l.cursorPepper, workspaceID, listCursor{CreatedAt: last.CreatedAt, ID: last.ID})
			break
		}
		aad := targetAssociatedData(record.Target.FormatVersion, workspaceID, record.ID,
			destination{scheme: record.Scheme, host: record.Host, port: record.Port})
		path, openErr := cryptobox.Open(l.materials.Signing, record.Target, aad)
		if openErr != nil {
			return ListResult{}, openErr
		}
		result.Items = append(result.Items, ListItem{
			ID: record.ID, URL: buildURL(record.Scheme, record.Host, record.Port, string(path)),
			Status: record.Status, EventTypes: record.EventTypes, CreatedAt: record.CreatedAt,
		})
		clear(path)
	}
	return result, err
}

type listCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func encodeListCursor(key [32]byte, workspaceID uuid.UUID, value listCursor) (string, error) {
	if key == ([32]byte{}) || workspaceID == uuid.Nil || value.ID == uuid.Nil || value.CreatedAt.IsZero() {
		return "", ErrInvalid
	}
	payload := make([]byte, 41, 73)
	payload[0] = 1
	copy(payload[1:17], workspaceID[:])
	binary.BigEndian.PutUint64(payload[17:25], uint64(value.CreatedAt.UnixNano()))
	copy(payload[25:41], value.ID[:])
	mac := endpointCursorMAC(key, payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac[:]...)), nil
}

func decodeListCursor(key [32]byte, workspaceID uuid.UUID, raw string) (listCursor, error) {
	if raw == "" {
		return listCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 73 || decoded[0] != 1 || key == ([32]byte{}) || workspaceID == uuid.Nil {
		return listCursor{}, ErrInvalid
	}
	expected := endpointCursorMAC(key, decoded[:41])
	if !hmac.Equal(expected[:], decoded[41:]) || !hmac.Equal(decoded[1:17], workspaceID[:]) {
		return listCursor{}, ErrInvalid
	}
	value := listCursor{CreatedAt: time.Unix(0, int64(binary.BigEndian.Uint64(decoded[17:25]))).UTC()}
	copy(value.ID[:], decoded[25:41])
	if value.ID == uuid.Nil || value.CreatedAt.IsZero() {
		return listCursor{}, ErrInvalid
	}
	return value, nil
}

func endpointCursorMAC(key [32]byte, payload []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("endpoint-cursor:v1\n"))
	_, _ = mac.Write(payload)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
