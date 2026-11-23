// Package crypto encrypts secrets at rest with a rotatable set of AES-256-GCM
// keys and derives HMAC secrets from the same keys.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// keySize is the AES-256 key length in bytes.
	keySize = 32
	// formatVersion is the first ciphertext byte, so the layout can evolve.
	formatVersion byte = 1
	// maxKeyIDLen keeps a key id short enough for its 1-byte length field.
	maxKeyIDLen = 32
	// headerFixedLen covers the version byte and the key-id length byte.
	headerFixedLen = 2
	// generatedIDBytes random bytes give the 8 hex chars of a generated key id.
	generatedIDBytes = 4
	// hmacSecretSize matches the SHA-256 output used for fosite HMAC signing.
	hmacSecretSize = 32
	// entrySep separates keyring entries in ENCRYPTION_KEYS.
	entrySep = ","
	// idSep separates a key id from its base64 key inside one entry.
	idSep = ":"
	// infoSep joins the HKDF label and the key id, so each key derives its own secret.
	infoSep = "/"
)

// keyIDPattern keeps ids printable; their length is checked against maxKeyIDLen.
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var (
	errEmptyKeyring    = errors.New("crypto: empty keyring")
	errShortCiphertext = errors.New("crypto: ciphertext too short")
	errDecrypt         = errors.New("crypto: decryption failed")
)

type key struct {
	id     string
	secret []byte
	aead   cipher.AEAD
	// header is the version | len(id) | id prefix of every ciphertext under this key.
	header []byte
}

// Keyring holds encryption keys in priority order; the first one is active
// for new writes, the others only decrypt and verify.
type Keyring struct {
	keys []key
	byID map[string]*key
}

// ParseKeyring parses a comma-separated list of `keyid:base64(32 bytes)`
// entries. Errors name the offending key id, never the key itself.
func ParseKeyring(s string) (*Keyring, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errEmptyKeyring
	}

	entries := strings.Split(s, entrySep)
	kr := &Keyring{
		keys: make([]key, 0, len(entries)),
		byID: make(map[string]*key, len(entries)),
	}

	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		id, encoded, ok := strings.Cut(strings.TrimSpace(entry), idSep)
		if !ok {
			return nil, fmt.Errorf("crypto: keyring entry %d: want keyid:base64", i+1)
		}

		if len(id) > maxKeyIDLen || !keyIDPattern.MatchString(id) {
			return nil, fmt.Errorf("crypto: keyring entry %d: key id must be 1-%d chars of [A-Za-z0-9_-]", i+1, maxKeyIDLen)
		}

		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("crypto: duplicate key id %q", id)
		}

		seen[id] = struct{}{}
		// The decode error is dropped on purpose: it can quote key material.
		secret, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %q: invalid base64", id)
		}

		if len(secret) != keySize {
			return nil, fmt.Errorf("crypto: key %q: got %d bytes, want %d", id, len(secret), keySize)
		}

		aead, err := newAEAD(secret)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %q: %w", id, err)
		}

		header, err := newHeader(id)
		if err != nil {
			return nil, err
		}

		kr.keys = append(kr.keys, key{id: id, secret: secret, aead: aead, header: header})
	}
	// byID points into kr.keys, so it is filled only once the slice stops growing.
	for i := range kr.keys {
		kr.byID[kr.keys[i].id] = &kr.keys[i]
	}

	return kr, nil
}

// newHeader builds the version | len(id) | id ciphertext prefix.
func newHeader(id string) ([]byte, error) {
	idLen := len(id)
	if idLen < 1 || idLen > maxKeyIDLen {
		return nil, fmt.Errorf("crypto: key id %q: length %d out of range", id, idLen)
	}

	header := make([]byte, 0, headerFixedLen+idLen)
	header = append(header, formatVersion, uint8(idLen))

	return append(header, id...), nil
}

func newAEAD(secret []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCM(block)
}

// ActiveID returns the id of the key used for new encryptions.
func (kr *Keyring) ActiveID() string {
	return kr.keys[0].id
}

// Encrypt seals plaintext under the active key. The output layout is
// version | len(keyid) | keyid | nonce | sealed. aad binds the ciphertext to
// its context (for example a row id) without being stored.
func (kr *Keyring) Encrypt(plaintext, aad []byte) ([]byte, error) {
	k := &kr.keys[0]
	nonceSize := k.aead.NonceSize()
	out := make([]byte, 0, len(k.header)+nonceSize+len(plaintext)+k.aead.Overhead())
	out = append(out, k.header...)
	nonceStart := len(out)

	out = out[:nonceStart+nonceSize]
	if _, err := rand.Read(out[nonceStart:]); err != nil {
		return nil, fmt.Errorf("crypto: read nonce: %w", err)
	}

	return k.aead.Seal(out, out[nonceStart:], plaintext, aad), nil
}

// Decrypt opens a ciphertext produced by Encrypt with whichever listed key
// sealed it.
func (kr *Keyring) Decrypt(ciphertext, aad []byte) ([]byte, error) {
	id, body, err := parseHeader(ciphertext)
	if err != nil {
		return nil, err
	}

	k, ok := kr.byID[id]
	if !ok {
		return nil, fmt.Errorf("crypto: unknown key id %q", id)
	}

	nonceSize := k.aead.NonceSize()
	if len(body) < nonceSize+k.aead.Overhead() {
		return nil, errShortCiphertext
	}

	plaintext, err := k.aead.Open(nil, body[:nonceSize], body[nonceSize:], aad)
	if err != nil {
		return nil, fmt.Errorf("%w under key %q", errDecrypt, id)
	}

	return plaintext, nil
}

// KeyID returns the id of the key that sealed ciphertext, whether or not that
// key is still in the keyring. rekey uses it to find rows to re-encrypt.
func (kr *Keyring) KeyID(ciphertext []byte) (string, error) {
	id, _, err := parseHeader(ciphertext)
	return id, err
}

func parseHeader(ciphertext []byte) (id string, body []byte, err error) {
	if len(ciphertext) < headerFixedLen {
		return "", nil, errShortCiphertext
	}

	if ciphertext[0] != formatVersion {
		return "", nil, fmt.Errorf("crypto: unsupported ciphertext version %d", ciphertext[0])
	}

	idLen := int(ciphertext[1])
	if idLen == 0 || len(ciphertext) < headerFixedLen+idLen {
		return "", nil, errShortCiphertext
	}

	end := headerFixedLen + idLen

	return string(ciphertext[headerFixedLen:end]), ciphertext[end:], nil
}

// HMACSecrets derives one 32-byte secret per key with HKDF-SHA256 (no salt,
// info = label/keyid). active comes from the active key; rotated keeps
// keyring order so tokens signed before a rotation still verify.
func (kr *Keyring) HMACSecrets(label string) (active []byte, rotated [][]byte) {
	secrets := make([][]byte, len(kr.keys))
	for i, k := range kr.keys {
		secret, err := hkdf.Key(sha256.New, k.secret, nil, label+infoSep+k.id, hmacSecretSize)
		if err != nil {
			// Only possible for lengths above 255*32 bytes, never for hmacSecretSize.
			panic(fmt.Sprintf("crypto: hkdf for key %q: %v", k.id, err))
		}

		secrets[i] = secret
	}

	return secrets[0], secrets[1:]
}

// GenerateKey returns a new random `keyid:base64` keyring entry.
func GenerateKey() (string, error) {
	id := make([]byte, generatedIDBytes)
	if _, err := rand.Read(id); err != nil {
		return "", fmt.Errorf("crypto: read key id: %w", err)
	}

	secret := make([]byte, keySize)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("crypto: read key: %w", err)
	}

	return hex.EncodeToString(id) + idSep + base64.StdEncoding.EncodeToString(secret), nil
}
