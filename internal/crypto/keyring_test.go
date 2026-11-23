package crypto

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// testKey returns a deterministic 32-byte key encoded for a keyring entry.
func testKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, keySize))
}

func mustParse(t *testing.T, s string) *Keyring {
	t.Helper()

	kr, err := ParseKeyring(s)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	return kr
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1)+",B:"+testKey(2))
	if got := kr.ActiveID(); got != "A" {
		t.Fatalf("ActiveID = %q, want A", got)
	}

	for _, plaintext := range [][]byte{nil, {}, []byte("refresh-token"), bytes.Repeat([]byte("x"), 4096)} {
		ct, err := kr.Encrypt(plaintext, []byte("user:42"))
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}

		if len(plaintext) > 0 && bytes.Contains(ct, plaintext) {
			t.Fatalf("ciphertext contains plaintext")
		}

		pt, err := kr.Decrypt(ct, []byte("user:42"))
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}

		if !bytes.Equal(pt, plaintext) {
			t.Fatalf("Decrypt = %q, want %q", pt, plaintext)
		}

		id, err := kr.KeyID(ct)
		if err != nil || id != "A" {
			t.Fatalf("KeyID = %q, %v; want A", id, err)
		}
	}
}

func TestEncrypt_FreshNonce(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1))

	a, err := kr.Encrypt([]byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}

	b, err := kr.Encrypt([]byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext are identical")
	}
}

func TestEncrypt_Layout(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "key-1:"+testKey(1))

	ct, err := kr.Encrypt([]byte("abc"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if ct[0] != 1 {
		t.Errorf("version = %d, want 1", ct[0])
	}

	if int(ct[1]) != len("key-1") || string(ct[2:7]) != "key-1" {
		t.Errorf("header = %q, want len 5 + key-1", ct[:7])
	}
	// header + nonce + plaintext + GCM tag (16 bytes).
	if want := 7 + 12 + 3 + 16; len(ct) != want {
		t.Errorf("len = %d, want %d", len(ct), want)
	}
}

func TestDecrypt_WrongAAD(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1))

	ct, err := kr.Encrypt([]byte("secret"), []byte("user:1"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := kr.Decrypt(ct, []byte("user:2")); err == nil {
		t.Fatal("Decrypt with wrong AAD succeeded")
	}
}

func TestDecrypt_Tampered(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1))

	ct, err := kr.Encrypt([]byte("secret"), nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"flipped tag":   append(append([]byte{}, ct[:len(ct)-1]...), ct[len(ct)-1]^0xff),
		"bad version":   append([]byte{2}, ct[1:]...),
		"truncated":     ct[:len(ct)-1],
		"header only":   ct[:3],
		"empty":         nil,
		"id overflow":   {1, 200, 'A'},
		"zero-length":   {1, 0},
		"no nonce room": ct[:3+5],
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := kr.Decrypt(in, nil); err == nil {
				t.Fatal("Decrypt succeeded")
			}
		})
	}
}

func TestDecrypt_OldKeyStillListed(t *testing.T) {
	t.Parallel()

	old := mustParse(t, "A:"+testKey(1))

	ct, err := old.Encrypt([]byte("token"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}

	rotated := mustParse(t, "B:"+testKey(2)+",A:"+testKey(1))
	if rotated.ActiveID() != "B" {
		t.Fatalf("ActiveID = %q, want B", rotated.ActiveID())
	}

	pt, err := rotated.Decrypt(ct, []byte("aad"))
	if err != nil || string(pt) != "token" {
		t.Fatalf("Decrypt = %q, %v", pt, err)
	}

	id, err := rotated.KeyID(ct)
	if err != nil || id != "A" {
		t.Fatalf("KeyID = %q, %v; want A", id, err)
	}

	fresh, err := rotated.Encrypt([]byte("token"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if id, _ := rotated.KeyID(fresh); id != "B" {
		t.Fatalf("new ciphertext KeyID = %q, want B", id)
	}
}

func TestDecrypt_KeyRemoved(t *testing.T) {
	t.Parallel()

	old := mustParse(t, "oldkey:"+testKey(1))

	ct, err := old.Encrypt([]byte("token"), nil)
	if err != nil {
		t.Fatal(err)
	}

	kr := mustParse(t, "newkey:"+testKey(2))

	_, err = kr.Decrypt(ct, nil)
	if err == nil {
		t.Fatal("Decrypt succeeded without the key")
	}

	if !strings.Contains(err.Error(), "oldkey") {
		t.Errorf("error %q does not name the key id", err)
	}

	if id, err := kr.KeyID(ct); err != nil || id != "oldkey" {
		t.Errorf("KeyID = %q, %v; want oldkey", id, err)
	}
}

func TestKeyID_Invalid(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1))
	for _, in := range [][]byte{nil, {1}, {2, 1, 'A'}, {1, 5, 'A'}} {
		if _, err := kr.KeyID(in); err == nil {
			t.Errorf("KeyID(%v) succeeded", in)
		}
	}
}

func TestParse_Invalid(t *testing.T) {
	t.Parallel()

	secret := testKey(7)
	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))

	cases := map[string]string{
		"empty":           "",
		"blank":           "  ",
		"bad base64":      "A:not*base64!",
		"16-byte key":     "A:" + short,
		"duplicate id":    "A:" + secret + ",A:" + testKey(8),
		"missing colon":   "A" + secret,
		"empty id":        ":" + secret,
		"empty key":       "A:",
		"trailing comma":  "A:" + secret + ",",
		"id with space":   "a b:" + secret,
		"id with dot":     "a.b:" + secret,
		"id too long":     strings.Repeat("a", 33) + ":" + secret,
		"extra colon key": "A:" + secret + ":x",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseKeyring(in)
			if err == nil {
				t.Fatal("ParseKeyring succeeded")
			}

			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), short) {
				t.Errorf("error %q leaks key material", err)
			}
		})
	}
}

func TestParse_Valid(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, " "+strings.Repeat("a", 32)+":"+testKey(1)+" , B_2-x:"+testKey(2))
	if got := kr.ActiveID(); got != strings.Repeat("a", 32) {
		t.Errorf("ActiveID = %q", got)
	}
}

func TestHMACSecrets_DifferPerLabel(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1)+",B:"+testKey(2)+",C:"+testKey(3))
	a1, r1 := kr.HMACSecrets("fosite-hmac")

	a2, r2 := kr.HMACSecrets("other")
	if len(a1) != 32 || len(a2) != 32 {
		t.Fatalf("len = %d, %d; want 32", len(a1), len(a2))
	}

	if bytes.Equal(a1, a2) {
		t.Error("active secret identical across labels")
	}

	if len(r1) != 2 || len(r2) != 2 {
		t.Fatalf("rotated = %d, %d; want 2", len(r1), len(r2))
	}

	for i := range r1 {
		if bytes.Equal(r1[i], r2[i]) {
			t.Errorf("rotated[%d] identical across labels", i)
		}

		if bytes.Equal(r1[i], a1) {
			t.Errorf("rotated[%d] equals active", i)
		}
	}

	if bytes.Equal(r1[0], r1[1]) {
		t.Error("rotated secrets identical across keys")
	}

	again, _ := kr.HMACSecrets("fosite-hmac")
	if !bytes.Equal(a1, again) {
		t.Error("HMACSecrets not deterministic")
	}

	// After rotation, the old active secret must still be offered.
	rotated := mustParse(t, "D:"+testKey(4)+",A:"+testKey(1))

	_, old := rotated.HMACSecrets("fosite-hmac")
	if len(old) != 1 || !bytes.Equal(old[0], a1) {
		t.Error("secret for key A changed after rotation")
	}
}

func TestHMACSecrets_SingleKey(t *testing.T) {
	t.Parallel()

	kr := mustParse(t, "A:"+testKey(1))

	active, rotated := kr.HMACSecrets("fosite-hmac")
	if len(active) != 32 || len(rotated) != 0 {
		t.Fatalf("active = %d bytes, rotated = %d; want 32, 0", len(active), len(rotated))
	}
}

func TestGenerateKey_Parses(t *testing.T) {
	t.Parallel()

	s1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	s2, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	if s1 == s2 {
		t.Fatal("GenerateKey returned the same key twice")
	}

	kr := mustParse(t, s1)

	id := kr.ActiveID()
	if len(id) != 8 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("key id = %q, want 8 hex chars", id)
	}

	ct, err := kr.Encrypt([]byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if pt, err := kr.Decrypt(ct, nil); err != nil || string(pt) != "x" {
		t.Fatalf("Decrypt = %q, %v", pt, err)
	}
}

func TestHMACSecrets_Derivation(t *testing.T) {
	t.Parallel()

	// Pins the derivation contract: HKDF-SHA256, nil salt, info = label + "/" + key id.
	kr := mustParse(t, "A:"+testKey(1))

	want, err := hkdf.Key(sha256.New, bytes.Repeat([]byte{1}, keySize), nil, "fosite-hmac/A", 32)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := kr.HMACSecrets("fosite-hmac"); !bytes.Equal(got, want) {
		t.Errorf("HMACSecrets = %x, want %x", got, want)
	}
}
