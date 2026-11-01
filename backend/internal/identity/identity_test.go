package identity

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestTokenHasEnoughEntropy(t *testing.T) {
	tok := NewToken()
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw)*8 < 256 {
		t.Fatalf("token %q: %d bits, err %v", tok, len(raw)*8, err)
	}
	if NewToken() == tok {
		t.Fatal("tokens repeat")
	}
}

// Хешируется UTF-8 строка токена, а не декодированные байты: так совместимы credential из
// текущего Node backend (§4).
func TestHashTokenHashesString(t *testing.T) {
	tok := "abc-DEF_123"
	want := sha256.Sum256([]byte(tok))
	if got := HashToken(tok); string(got) != string(want[:]) {
		t.Fatalf("hash mismatch")
	}
}

func TestValidators(t *testing.T) {
	if validTimezone("Europe/Moscow") != "" || validTimezone("Mars/Base") == "" || validTimezone("Local") == "" {
		t.Fatal("timezone validation")
	}
	if validNickname("Аня") != "" || validNickname("") == "" || validNickname("a\x07") == "" {
		t.Fatal("nickname validation")
	}
}
