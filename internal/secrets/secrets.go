// Package secrets encrypts sensitive values before persistence.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	formatVersion byte = 2
	nonceSize          = 12
	tagSize            = 16
	keyIDSize          = 8
	headerSize         = 1 + keyIDSize + nonceSize
	keySize            = 32
	keyInfo            = "bloom/secrets/aes-256-gcm/v2"
	keyIDInfo          = "bloom/secrets/key-id/v1"
)

var (
	// ErrMalformedCiphertext reports an invalid versioned envelope.
	ErrMalformedCiphertext = errors.New("malformed ciphertext")
	// ErrUnsupportedVersion reports a ciphertext format this binary cannot read.
	ErrUnsupportedVersion = errors.New("unsupported ciphertext version")
	// ErrWrongKey reports an envelope created with a different master key.
	ErrWrongKey = errors.New("ciphertext key id does not match active key")
	// ErrAuthentication reports modified ciphertext or associated metadata.
	ErrAuthentication = errors.New("ciphertext authentication failed")
)

// Context binds a ciphertext to its storage purpose and record metadata.
type Context struct {
	Purpose  string
	RecordID string
	Kind     string
	BaseURL  string
}

// Cipher encrypts and authenticates values with a purpose-derived AES-256 key.
type Cipher struct {
	aead  cipher.AEAD
	keyID [keyIDSize]byte
	rand  io.Reader
}

// New derives an encryption key and non-secret key identifier from master.
func New(master []byte) (*Cipher, error) {
	return newCipher(master, rand.Reader)
}

func newCipher(master []byte, random io.Reader) (*Cipher, error) {
	if len(master) == 0 || random == nil {
		return nil, errors.New("secrets: master key and random source are required")
	}
	key, err := hkdf.Key(sha256.New, master, nil, keyInfo, keySize)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}
	defer clear(key)
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	identifier, err := hkdf.Key(sha256.New, master, nil, keyIDInfo, keyIDSize)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key id: %w", err)
	}
	var keyID [keyIDSize]byte
	copy(keyID[:], identifier)
	clear(identifier)
	return &Cipher{aead: aead, keyID: keyID, rand: random}, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("construct AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construct GCM: %w", err)
	}
	return aead, nil
}

// Encrypt returns version || key-id || nonce || authenticated ciphertext.
func (c *Cipher) Encrypt(plaintext []byte, metadata Context) ([]byte, error) {
	if err := c.validate(metadata); err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(c.rand, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	out := make([]byte, headerSize, headerSize+len(plaintext)+c.aead.Overhead())
	out[0] = formatVersion
	copy(out[1:1+keyIDSize], c.keyID[:])
	copy(out[1+keyIDSize:], nonce)
	return c.aead.Seal(out, nonce, plaintext, associatedData(metadata)), nil
}

// KeyID returns the non-secret identifier embedded in new envelopes.
func (c *Cipher) KeyID() string {
	if c == nil {
		return ""
	}
	return hex.EncodeToString(c.keyID[:])
}

// Decrypt verifies the key identity, metadata, and ciphertext before opening it.
func (c *Cipher) Decrypt(ciphertext []byte, metadata Context) ([]byte, error) {
	if err := c.validate(metadata); err != nil {
		return nil, err
	}
	if len(ciphertext) < headerSize+tagSize {
		return nil, ErrMalformedCiphertext
	}
	if ciphertext[0] != formatVersion {
		return nil, ErrUnsupportedVersion
	}
	if subtle.ConstantTimeCompare(ciphertext[1:1+keyIDSize], c.keyID[:]) != 1 {
		return nil, ErrWrongKey
	}
	nonce := ciphertext[1+keyIDSize : headerSize]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext[headerSize:], associatedData(metadata))
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func (c *Cipher) validate(metadata Context) error {
	if c == nil || c.aead == nil || c.rand == nil {
		return errors.New("secrets: uninitialized cipher")
	}
	if metadata.Purpose == "" || metadata.RecordID == "" || metadata.Kind == "" || metadata.BaseURL == "" {
		return errors.New("secrets: complete associated data is required")
	}
	return nil
}

func associatedData(metadata Context) []byte {
	data := []byte{formatVersion}
	data = appendField(data, metadata.Purpose)
	data = appendField(data, metadata.RecordID)
	data = appendField(data, metadata.Kind)
	return appendField(data, metadata.BaseURL)
}

func appendField(target []byte, value string) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value))) //nolint:gosec // Inputs are bounded before encryption.
	target = append(target, size[:]...)
	return append(target, value...)
}
