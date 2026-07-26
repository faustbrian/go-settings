package settings_test

import (
	"bytes"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
)

type xorCipher struct{ key byte }

func (xorCipher) ID() string { return "test-xor" }
func (cipher xorCipher) Seal(plain []byte) ([]byte, error) {
	return xorBytes(plain, cipher.key), nil
}
func (cipher xorCipher) Open(sealed []byte) ([]byte, error) {
	return xorBytes(sealed, cipher.key), nil
}
func xorBytes(value []byte, key byte) []byte {
	result := append([]byte(nil), value...)
	for index := range result {
		result[index] ^= key
	}
	return result
}

func TestEncryptionCodecDelegatesKeysAndCryptographyToCaller(t *testing.T) {
	t.Parallel()

	codec := settings.NewEncryptionCodec(settings.StringCodec{}, xorCipher{key: 0x5a}, 3)
	encoded, err := codec.Encode("secret")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	decoded, err := codec.Decode(encoded)
	if err != nil || decoded != "secret" {
		t.Fatalf("decode = %q, %v", decoded, err)
	}
	if codec.ID() != "encrypted:test-xor:string" || codec.Version() != 3 {
		t.Fatalf("codec contract = %s@%d", codec.ID(), codec.Version())
	}
}
