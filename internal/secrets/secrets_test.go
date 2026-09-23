package secrets

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

var testMasterKey = []byte("0123456789abcdef0123456789abcdef")

var testContext = Context{
	Purpose: "media-server-api-key", RecordID: "11111111-1111-4111-8111-111111111111",
	Kind: "jellyfin", BaseURL: "https://media.example.test",
}

func TestCipherRoundTrip(t *testing.T) {
	cipher := testCipher(t, testMasterKey)
	ciphertext, err := cipher.Encrypt([]byte("jellyfin-api-key"), testContext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := cipher.Decrypt(ciphertext, testContext)
	if err != nil || string(plaintext) != "jellyfin-api-key" {
		t.Fatalf("Decrypt = %q, %v", plaintext, err)
	}
}

func TestCipherRejectsWrongKeySeparatelyFromCorruption(t *testing.T) {
	cipher := testCipher(t, testMasterKey)
	ciphertext, err := cipher.Encrypt([]byte("credential"), testContext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 1
	if _, err := cipher.Decrypt(tampered, testContext); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Decrypt(tampered) = %v, want ErrAuthentication", err)
	}
	other := testCipher(t, []byte("abcdef0123456789abcdef0123456789"))
	if _, err := other.Decrypt(ciphertext, testContext); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("Decrypt(wrong key) = %v, want ErrWrongKey", err)
	}
}

func TestCipherBindsEveryMetadataField(t *testing.T) {
	cipher := testCipher(t, testMasterKey)
	ciphertext, err := cipher.Encrypt([]byte("credential"), testContext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tests := []Context{
		{Purpose: "oidc-client-secret", RecordID: testContext.RecordID, Kind: testContext.Kind, BaseURL: testContext.BaseURL},
		{Purpose: testContext.Purpose, RecordID: "22222222-2222-4222-8222-222222222222", Kind: testContext.Kind, BaseURL: testContext.BaseURL},
		{Purpose: testContext.Purpose, RecordID: testContext.RecordID, Kind: "emby", BaseURL: testContext.BaseURL},
		{Purpose: testContext.Purpose, RecordID: testContext.RecordID, Kind: testContext.Kind, BaseURL: "https://attacker.example.test"},
	}
	for _, metadata := range tests {
		if _, err := cipher.Decrypt(ciphertext, metadata); !errors.Is(err, ErrAuthentication) {
			t.Errorf("Decrypt(%+v) = %v, want ErrAuthentication", metadata, err)
		}
	}
}

func TestCipherRejectsSwappedCiphertexts(t *testing.T) {
	cipher := testCipher(t, testMasterKey)
	first, err := cipher.Encrypt([]byte("first"), testContext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	secondContext := testContext
	secondContext.RecordID = "22222222-2222-4222-8222-222222222222"
	if _, err := cipher.Decrypt(first, secondContext); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Decrypt(swapped) = %v, want ErrAuthentication", err)
	}
}

func TestCipherVersionAndLayout(t *testing.T) {
	cipher, err := newCipher(testMasterKey, bytes.NewReader(bytes.Repeat([]byte{0x5a}, nonceSize)))
	if err != nil {
		t.Fatalf("newCipher: %v", err)
	}
	ciphertext, err := cipher.Encrypt([]byte("key"), testContext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext[0] != formatVersion || len(ciphertext) != headerSize+3+tagSize {
		t.Fatalf("envelope version=%d length=%d", ciphertext[0], len(ciphertext))
	}
	unsupported := bytes.Clone(ciphertext)
	unsupported[0]++
	if _, err := cipher.Decrypt(unsupported, testContext); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Decrypt(version) = %v", err)
	}
	if _, err := cipher.Decrypt(ciphertext[:headerSize], testContext); !errors.Is(err, ErrMalformedCiphertext) {
		t.Fatalf("Decrypt(short) = %v", err)
	}
}

func TestCipherNonceSourceFailures(t *testing.T) {
	sentinel := errors.New("random source failed")
	tests := []struct {
		name   string
		reader io.Reader
		want   error
	}{
		{name: "short reader", reader: bytes.NewReader(make([]byte, nonceSize-1)), want: io.ErrUnexpectedEOF},
		{name: "failing reader", reader: errorReader{err: sentinel}, want: sentinel},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cipher, err := newCipher(testMasterKey, testCase.reader)
			if err != nil {
				t.Fatalf("newCipher: %v", err)
			}
			if _, err := cipher.Encrypt([]byte("key"), testContext); !errors.Is(err, testCase.want) {
				t.Fatalf("Encrypt error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestCipherSequentialEncryptionsUseDistinctNonces(t *testing.T) {
	random := append(bytes.Repeat([]byte{0x11}, nonceSize), bytes.Repeat([]byte{0x22}, nonceSize)...)
	cipher, err := newCipher(testMasterKey, bytes.NewReader(random))
	if err != nil {
		t.Fatalf("newCipher: %v", err)
	}
	first, err := cipher.Encrypt([]byte("key"), testContext)
	if err != nil {
		t.Fatalf("first Encrypt: %v", err)
	}
	second, err := cipher.Encrypt([]byte("key"), testContext)
	if err != nil {
		t.Fatalf("second Encrypt: %v", err)
	}
	nonceStart := 1 + keyIDSize
	if bytes.Equal(first[nonceStart:headerSize], second[nonceStart:headerSize]) {
		t.Fatal("sequential encryptions reused a nonce")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestDeviceIDIsStableAndKeySpecific(t *testing.T) {
	first, err := DeviceID(testMasterKey)
	if err != nil {
		t.Fatalf("DeviceID: %v", err)
	}
	again, err := DeviceID(testMasterKey)
	if err != nil {
		t.Fatalf("DeviceID again: %v", err)
	}
	other, err := DeviceID([]byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatalf("DeviceID other: %v", err)
	}
	if first != again || first == other || len(first) != 36 {
		t.Fatalf("device ids first=%q again=%q other=%q", first, again, other)
	}
}

func TestDeviceIDRejectsEmptyMasterSecret(t *testing.T) {
	if _, err := DeviceID(nil); err == nil {
		t.Fatal("DeviceID(nil) succeeded")
	}
}

func FuzzCiphertextDecoder(f *testing.F) {
	cipher := testCipher(f, testMasterKey)
	valid, err := cipher.Encrypt([]byte("seed"), testContext)
	if err != nil {
		f.Fatalf("Encrypt: %v", err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{formatVersion})
	f.Fuzz(func(t *testing.T, ciphertext []byte) {
		if _, err := cipher.Decrypt(ciphertext, testContext); err != nil {
			return
		}
	})
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

func testCipher(t testingTB, key []byte) *Cipher {
	t.Helper()
	cipher, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cipher
}
