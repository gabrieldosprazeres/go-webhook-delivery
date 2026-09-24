package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type keyringDocument struct {
	FormatVersion     int                  `json:"format_version"`
	PrimaryKeyVersion int                  `json:"primary_key_version"`
	Keys              []keyringDocumentKey `json:"keys"`
}

type keyringDocumentKey struct {
	Version  int    `json:"version"`
	Material string `json:"material"`
}

func validateKeyringFile(name, path string) (map[[keyMaterialBytes]byte]struct{}, error) {
	contents, err := readSecretFile(name, path)
	if err != nil {
		return nil, err
	}
	defer clear(contents)
	document, err := decodeKeyring(name, contents)
	if err != nil {
		return nil, err
	}
	return validateKeyringKeys(name, document)
}

func decodeKeyring(name string, contents []byte) (keyringDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if err := decoder.Decode(&document); err != nil {
		return document, fmt.Errorf("config: %s must contain valid keyring JSON", name)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return document, fmt.Errorf("config: %s must contain one JSON document", name)
	}
	if document.FormatVersion != 1 || document.PrimaryKeyVersion <= 0 || len(document.Keys) == 0 || len(document.Keys) > 8 {
		return document, fmt.Errorf("config: %s has unsupported keyring metadata", name)
	}
	return document, nil
}

func validateKeyringKeys(name string, document keyringDocument) (map[[keyMaterialBytes]byte]struct{}, error) {
	versions := make(map[int]struct{}, len(document.Keys))
	materials := make(map[[keyMaterialBytes]byte]struct{}, len(document.Keys))
	primaryFound := false
	for _, key := range document.Keys {
		material, err := validateKeyringKey(name, key, versions, materials)
		if err != nil {
			return nil, err
		}
		versions[key.Version], materials[material] = struct{}{}, struct{}{}
		primaryFound = primaryFound || key.Version == document.PrimaryKeyVersion
	}
	if !primaryFound {
		return nil, fmt.Errorf("config: %s primary key version does not exist", name)
	}
	return materials, nil
}

func validateKeyringKey(name string, key keyringDocumentKey, versions map[int]struct{}, materials map[[keyMaterialBytes]byte]struct{}) ([keyMaterialBytes]byte, error) {
	var material [keyMaterialBytes]byte
	if key.Version <= 0 {
		return material, fmt.Errorf("config: %s contains an invalid key version", name)
	}
	if _, duplicate := versions[key.Version]; duplicate {
		return material, fmt.Errorf("config: %s contains a duplicate key version", name)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(key.Material)
	if err != nil || len(decoded) != keyMaterialBytes {
		clear(decoded)
		return material, fmt.Errorf("config: %s keys must contain exactly 32 bytes of base64url key material", name)
	}
	copy(material[:], decoded)
	clear(decoded)
	if _, duplicate := materials[material]; duplicate {
		return material, fmt.Errorf("config: %s contains duplicate key material", name)
	}
	return material, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}
