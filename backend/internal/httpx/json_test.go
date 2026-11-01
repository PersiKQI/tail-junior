package httpx

import (
	"testing"

	"teyljunior/internal/core"
)

type sample struct {
	A string `json:"a"`
	N int    `json:"n"`
}

func TestDecodeRejects(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"empty":            {``, "INVALID_JSON"},
		"duplicate key":    {`{"a":"x","a":"y"}`, "INVALID_JSON"},
		"nested duplicate": {`{"a":"x","n":1,"z":{"k":1,"k":2}}`, "INVALID_JSON"},
		"two values":       {`{"a":"x"}{"a":"y"}`, "INVALID_JSON"},
		"array":            {`["a"]`, "INVALID_JSON"},
		"invalid utf8":     {"{\"a\":\"\xff\"}", "INVALID_JSON"},
		"unknown field":    {`{"a":"x","b":1}`, "VALIDATION_FAILED"},
		"fractional int":   {`{"n":1.5}`, "VALIDATION_FAILED"},
		"wrong type":       {`{"a":1}`, "VALIDATION_FAILED"},
		"syntax":           {`{"a":}`, "INVALID_JSON"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var v sample
			_, err := Decode([]byte(c.body), &v)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := core.AsError(err).Code; got != c.code {
				t.Fatalf("code = %s, want %s", got, c.code)
			}
		})
	}
}

// Канонический вид не зависит от порядка ключей и пробелов: одинаковый запрос с тем же
// Idempotency-Key даёт тот же хеш.
func TestDecodeCanonical(t *testing.T) {
	var v1, v2 sample
	c1, err := Decode([]byte(`{"a":"x","n":2}`), &v1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Decode([]byte("{ \"n\" : 2,\n \"a\":\"x\" }"), &v2)
	if err != nil {
		t.Fatal(err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("canonical differ: %s vs %s", c1, c2)
	}
	if v1 != v2 || v1.A != "x" || v1.N != 2 {
		t.Fatalf("decoded %+v %+v", v1, v2)
	}
}
