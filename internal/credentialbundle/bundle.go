// Package credentialbundle encrypts build-time mTLS credentials so their PEM
// representation is not stored verbatim in an executor binary.
package credentialbundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const maxCredentialSize = 64 << 10

// Seal encrypts a certificate, private key, and CA bundle with a fresh AES-256
// key and nonce. The returned hexadecimal strings are safe linker inputs.
func Seal(certificatePEM, privateKeyPEM, caPEM []byte) (string, string, error) {
	plaintext, err := marshal(certificatePEM, privateKeyPEM, caPEM)
	if err != nil {
		return "", "", err
	}
	defer Zero(plaintext)
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", "", fmt.Errorf("generate credential key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return hex.EncodeToString(key), hex.EncodeToString(sealed), nil
}

// Open authenticates and decrypts a hexadecimal credential bundle.
func Open(keyHex, bundleHex string) (certificatePEM, privateKeyPEM, caPEM []byte, err error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, nil, nil, errors.New("invalid embedded credential key")
	}
	defer Zero(key)
	sealed, err := hex.DecodeString(bundleHex)
	if err != nil {
		return nil, nil, nil, errors.New("invalid embedded credential bundle")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, nil, nil, errors.New("embedded credential bundle is truncated")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, nil, nil, errors.New("embedded credential bundle authentication failed")
	}
	defer Zero(plaintext)
	return unmarshal(plaintext)
}

// Zero overwrites sensitive byte slices after they are parsed or encrypted.
func Zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

// marshal encodes exactly three bounded credentials with length prefixes.
func marshal(values ...[]byte) ([]byte, error) {
	total := 0
	for _, value := range values {
		if len(value) == 0 || len(value) > maxCredentialSize {
			return nil, errors.New("credential files must be non-empty and at most 64 KiB")
		}
		total += 4 + len(value)
	}
	result := make([]byte, 0, total)
	for _, value := range values {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		result = append(result, size[:]...)
		result = append(result, value...)
	}
	return result, nil
}

// unmarshal decodes exactly three bounded length-prefixed credentials.
func unmarshal(value []byte) ([]byte, []byte, []byte, error) {
	result := make([][]byte, 0, 3)
	for range 3 {
		if len(value) < 4 {
			return nil, nil, nil, errors.New("embedded credential bundle is malformed")
		}
		size := int(binary.BigEndian.Uint32(value[:4]))
		value = value[4:]
		if size == 0 || size > maxCredentialSize || len(value) < size {
			return nil, nil, nil, errors.New("embedded credential bundle contains an invalid credential")
		}
		result = append(result, append([]byte(nil), value[:size]...))
		value = value[size:]
	}
	if len(value) != 0 {
		return nil, nil, nil, errors.New("embedded credential bundle contains trailing data")
	}
	return result[0], result[1], result[2], nil
}
