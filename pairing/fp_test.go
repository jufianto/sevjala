package pairing

import "testing"

func TestParseFP(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"3f2a9b1c77de0a41", "3f2a9b1c77de0a41", true},
		{"sha256:3F2A9B1C77DE0A41", "3f2a9b1c77de0a41", true},
		{" 3f2a-9b1c 77de:0a41 ", "3f2a9b1c77de0a41", true},
		{"3f2a9b1c77de0a41ff", "3f2a9b1c77de0a41ff", true},
		{"3f2a9b1c77de0a4", "", false},
		{"3f2a9b1c77de0a4g", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseFP(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseFP(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestShortAndMatchFP(t *testing.T) {
	fp := "sha256:3f2a9b1c77de0a41aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := ShortFP(fp); got != "3f2a9b1c77de0a41" {
		t.Errorf("ShortFP = %q", got)
	}
	if !MatchFP(fp, "3f2a9b1c77de0a41") || MatchFP(fp, "0000000000000000") {
		t.Error("MatchFP")
	}
}
