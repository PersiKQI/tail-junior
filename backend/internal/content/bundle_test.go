package content

import (
	"encoding/json"
	"strings"
	"testing"

	bankfiles "teyljunior/content"
)

func TestEmbeddedBundleIsConsistent(t *testing.T) {
	b, err := ParseBundle(bankfiles.Program, bankfiles.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(b.Program.Items); got != 33 {
		t.Fatalf("items = %d, want 33", got)
	}
	if b.Program.PublicationStatus != "draft" {
		t.Fatalf("publicationStatus = %q: восстановленный банк не должен выдаваться за опубликованный", b.Program.PublicationStatus)
	}
}

// L24: два действия совместимы с общим объяснением; третье — нет (§6).
func TestGradeUsesExplicitPairs(t *testing.T) {
	b, err := ParseBundle(bankfiles.Program, bankfiles.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	var g Grading
	for _, it := range b.Program.Items {
		if it.ItemID == "L24" {
			g = b.GradingOf(it)
		}
	}
	cases := []struct {
		choice, reason             string
		choiceOK, reasonOK, passed bool
	}{
		{"limits", "assumptions", true, true, true},
		{"buffer", "assumptions", true, true, true},
		{"exact", "assumptions", false, true, false},
		{"limits", "math", true, false, false},
	}
	for _, c := range cases {
		co, ro, p := g.Grade(c.choice, c.reason)
		if co != c.choiceOK || ro != c.reasonOK || p != c.passed {
			t.Errorf("Grade(%s,%s) = %v,%v,%v want %v,%v,%v", c.choice, c.reason, co, ro, p, c.choiceOK, c.reasonOK, c.passed)
		}
	}
}

func TestAdapterWithoutPairsIsRejected(t *testing.T) {
	var a map[string]any
	if err := json.Unmarshal(bankfiles.Adapter, &a); err != nil {
		t.Fatal(err)
	}
	delete(a["items"].(map[string]any), "L24")
	broken, _ := json.Marshal(a)
	_, err := ParseBundle(bankfiles.Program, broken)
	if err == nil || !strings.Contains(err.Error(), "L24") {
		t.Fatalf("err = %v, want rejection naming L24", err)
	}
}

func TestPairMustMatchAcceptedSets(t *testing.T) {
	var a map[string]any
	if err := json.Unmarshal(bankfiles.Adapter, &a); err != nil {
		t.Fatal(err)
	}
	a["items"].(map[string]any)["L01"] = map[string]any{"allowedPairs": []any{[]any{"max", "deadline"}}}
	broken, _ := json.Marshal(a)
	if _, err := ParseBundle(bankfiles.Program, broken); err == nil {
		t.Fatal("pair outside accepted sets must be rejected")
	}
}

func TestUnknownFieldInBankIsRejected(t *testing.T) {
	broken := strings.Replace(string(bankfiles.Program), `"lineageId"`, `"secret": 1, "lineageId"`, 1)
	if _, err := ParseBundle([]byte(broken), bankfiles.Adapter); err == nil {
		t.Fatal("unknown field must be rejected")
	}
}
