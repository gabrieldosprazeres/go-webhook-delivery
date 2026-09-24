// Package cryptobox provides versioned authenticated encryption and startup key loading.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

const FormatVersion int16 = 1

type Envelope struct {
	FormatVersion int16
	KEKVersion    int16
	Nonce         []byte
	Ciphertext    []byte
}

func Seal(keyring Keyring, plaintext, aad []byte) (Envelope, error) {
	gcm, keyVersion, err := primaryCipher(keyring)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, errors.New("cryptobox: generate nonce")
	}
	return Envelope{FormatVersion: FormatVersion, KEKVersion: keyVersion, Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, plaintext, aad)}, nil
}

func Open(keyring Keyring, envelope Envelope, aad []byte) ([]byte, error) {
	if envelope.FormatVersion != FormatVersion {
		return nil, errors.New("cryptobox: unsupported format")
	}
	key, ok := keyring.Keys[envelope.KEKVersion]
	if !ok {
		return nil, errors.New("cryptobox: key unavailable")
	}
	gcm, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("cryptobox: authentication failed")
	}
	return plaintext, nil
}

func primaryCipher(keyring Keyring) (cipher.AEAD, int16, error) {
	key, ok := keyring.Keys[keyring.Primary]
	if !ok {
		return nil, 0, errors.New("cryptobox: primary key unavailable")
	}
	gcm, err := newCipher(key)
	return gcm, keyring.Primary, err
}

func newCipher(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, errors.New("cryptobox: initialize cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cryptobox: initialize gcm")
	}
	return gcm, nil
}

// AAD produces an unambiguous length-prefixed sequence.
func AAD(parts ...string) []byte {
	size := 0
	for _, part := range parts {
		size += 4 + len(part)
	}
	result := make([]byte, 0, size)
	var length [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		result = append(result, length[:]...)
		result = append(result, part...)
	}
	return result
}

func (e Envelope) Validate() error {
	if e.FormatVersion != FormatVersion || e.KEKVersion <= 0 || len(e.Nonce) != 12 || len(e.Ciphertext) < 16 {
		return fmt.Errorf("cryptobox: invalid envelope")
	}
	return nil
}
