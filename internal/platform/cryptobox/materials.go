package cryptobox

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

type Keyring struct {
	Primary int16
	Keys    map[int16][32]byte
}

type Materials struct {
	AuthPepper        [32]byte
	IdempotencyPepper [32]byte
	FingerprintPepper [32]byte
	RateLimitPepper   [32]byte
	CursorPepper      [32]byte
	SessionPepper     [32]byte
	CSRFPepper        [32]byte
	Payload           Keyring
	Signing           Keyring
}

type keyringDocument struct {
	FormatVersion     int `json:"format_version"`
	PrimaryKeyVersion int `json:"primary_key_version"`
	Keys              []struct {
		Version  int    `json:"version"`
		Material string `json:"material"`
	} `json:"keys"`
}

// Ready confirms that both in-memory keyrings still have usable primary keys.
func (m Materials) Ready() error {
	if _, ok := m.Payload.Keys[m.Payload.Primary]; !ok || m.Payload.Primary <= 0 {
		return errors.New("cryptobox: payload keyring unavailable")
	}
	if _, ok := m.Signing.Keys[m.Signing.Primary]; !ok || m.Signing.Primary <= 0 {
		return errors.New("cryptobox: signing keyring unavailable")
	}
	return nil
}

// Load returns deterministic, explicitly non-production material locally and mounted material in production.
func Load(profile config.Profile, files config.SecretFiles) (Materials, error) {
	if profile != config.ProfileProduction {
		return developmentMaterials(), nil
	}
	peppers, err := readPeppers(files)
	if err != nil {
		return Materials{}, err
	}
	payload, err := readKeyring(files.PayloadKeyring)
	if err != nil {
		return Materials{}, err
	}
	signing, err := readKeyring(files.SigningKeyring)
	if err != nil {
		return Materials{}, err
	}
	peppers.Payload, peppers.Signing = payload, signing
	return peppers, nil
}

func readPeppers(files config.SecretFiles) (Materials, error) {
	var result Materials
	var err error
	if files.AuthPepper == "" && files.IdempotencyPepper == "" && files.FingerprintPepper == "" &&
		files.RateLimitPepper == "" && files.CursorPepper == "" {
		return result, nil
	}
	paths := []string{files.AuthPepper, files.IdempotencyPepper, files.FingerprintPepper,
		files.RateLimitPepper, files.CursorPepper}
	targets := []*[32]byte{&result.AuthPepper, &result.IdempotencyPepper, &result.FingerprintPepper,
		&result.RateLimitPepper, &result.CursorPepper}
	for index, path := range paths {
		pepper, err := readPepper(path)
		if err != nil {
			return Materials{}, err
		}
		*targets[index] = pepper
	}
	if files.SessionPepper != "" {
		result.SessionPepper, err = readPepper(files.SessionPepper)
		if err != nil {
			return Materials{}, err
		}
	}
	if files.CSRFPepper != "" {
		result.CSRFPepper, err = readPepper(files.CSRFPepper)
		if err != nil {
			return Materials{}, err
		}
	}
	return result, nil
}

func developmentMaterials() Materials {
	key := func(label string) [32]byte { return sha256.Sum256([]byte("wde-local-only:" + label)) }
	return Materials{
		AuthPepper: key("auth"), IdempotencyPepper: key("idempotency"), FingerprintPepper: key("fingerprint"),
		RateLimitPepper: key("rate-limit"),
		CursorPepper:    key("cursor"),
		SessionPepper:   key("console-session"),
		CSRFPepper:      key("console-csrf"),
		Payload:         Keyring{Primary: 1, Keys: map[int16][32]byte{1: key("payload-kek")}},
		Signing:         Keyring{Primary: 1, Keys: map[int16][32]byte{1: key("signing-kek")}},
	}
}

func readPepper(path string) ([32]byte, error) {
	var result [32]byte
	raw, err := secureRead(path)
	if err != nil {
		return result, errors.New("cryptobox: cannot read pepper")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(trimV1(string(raw)))
	clear(raw)
	if err != nil || len(decoded) != 32 {
		clear(decoded)
		return result, errors.New("cryptobox: invalid pepper")
	}
	copy(result[:], decoded)
	clear(decoded)
	return result, nil
}

func trimV1(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ') {
		value = value[:len(value)-1]
	}
	if len(value) >= 3 && value[:3] == "v1:" {
		return value[3:]
	}
	return ""
}

func readKeyring(path string) (Keyring, error) {
	raw, err := secureRead(path)
	if err != nil {
		return Keyring{}, errors.New("cryptobox: cannot read keyring")
	}
	defer clear(raw)
	document, err := decodeKeyring(raw)
	if err != nil {
		return Keyring{}, err
	}
	return document.keyring()
}

func decodeKeyring(raw []byte) (keyringDocument, error) {
	var document keyringDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return keyringDocument{}, errors.New("cryptobox: invalid keyring")
	}
	if document.FormatVersion != 1 || len(document.Keys) < 1 || len(document.Keys) > 8 {
		return keyringDocument{}, errors.New("cryptobox: invalid keyring")
	}
	return document, nil
}

func (document keyringDocument) keyring() (Keyring, error) {
	result := Keyring{Primary: int16(document.PrimaryKeyVersion), Keys: make(map[int16][32]byte, len(document.Keys))}
	materials := make(map[[32]byte]struct{}, len(document.Keys))
	for _, entry := range document.Keys {
		key, err := decodeKey(entry.Version, entry.Material)
		if err != nil {
			return Keyring{}, err
		}
		if _, duplicate := result.Keys[int16(entry.Version)]; duplicate {
			return Keyring{}, errors.New("cryptobox: duplicate key version")
		}
		if _, duplicate := materials[key]; duplicate {
			return Keyring{}, errors.New("cryptobox: duplicate key material")
		}
		result.Keys[int16(entry.Version)], materials[key] = key, struct{}{}
	}
	if _, ok := result.Keys[result.Primary]; !ok {
		return Keyring{}, errors.New("cryptobox: missing primary key")
	}
	return result, nil
}

func decodeKey(version int, material string) ([32]byte, error) {
	var key [32]byte
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(material)
	if err != nil || len(decoded) != 32 || version <= 0 || version > 32767 {
		clear(decoded)
		return key, errors.New("cryptobox: invalid keyring key")
	}
	copy(key[:], decoded)
	clear(decoded)
	return key, nil
}
