package tokenize

import "testing"

func TestMakeIsDeterministic(t *testing.T) {
	a := Make("PERSON", "Jane Doe", "salt")
	b := Make("PERSON", "Jane Doe", "salt")
	if a != b {
		t.Fatalf("expected deterministic token, got %q and %q", a, b)
	}
}

func TestMakeVariesWithSaltAndValue(t *testing.T) {
	base := Make("PERSON", "Jane Doe", "salt")
	if Make("PERSON", "Jane Doe", "other") == base {
		t.Error("token should change with salt")
	}
	if Make("PERSON", "John Doe", "salt") == base {
		t.Error("token should change with value")
	}
	if Make("EMAIL_ADDRESS", "Jane Doe", "salt") == base {
		t.Error("token should change with entity type")
	}
}

func TestMakeMatchesPattern(t *testing.T) {
	cases := []struct{ entity, value string }{
		{"PERSON", "Jane"},
		{"EMAIL_ADDRESS", "a@b.com"},
		{"US_SSN", "123-45-6789"},
		{"weird entity!!", "x"}, // normalized
		{"", "x"},               // empty -> PII
	}
	for _, c := range cases {
		tok := Make(c.entity, c.value, "s")
		if !Pattern.MatchString(tok) {
			t.Errorf("token %q (entity %q) does not match Pattern", tok, c.entity)
		}
		if len(tok) > MaxLen {
			t.Errorf("token %q exceeds MaxLen %d", tok, MaxLen)
		}
	}
}

func TestPatternDoesNotMatchPlainBrackets(t *testing.T) {
	if Pattern.MatchString("[[1, 2, 3]]") {
		t.Error("plain double brackets should not match the token pattern")
	}
}
