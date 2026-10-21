package prompt

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		budget int
		want   string
	}{
		{"héllo", 5, "héllo"},
		{"héllo", 2, "hé\n[… truncated 3 chars]"},
		{"", 0, ""},
	}
	for _, tt := range tests {
		if got := Truncate(tt.s, tt.budget); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.s, tt.budget, got, tt.want)
		}
	}
}

func TestFitLines(t *testing.T) {
	lines := []string{"aaaa", "bbbb", "cccc"}
	if got, want := FitLines(lines, 10), "aaaa\nbbbb\n[… 1 more files]\n"; got != want {
		t.Errorf("FitLines = %q, want %q", got, want)
	}
	if got, want := FitLines(lines, 100), "aaaa\nbbbb\ncccc\n"; got != want {
		t.Errorf("FitLines = %q, want %q", got, want)
	}
}

func TestFitSummariesKeepsTheFirst(t *testing.T) {
	got := FitSummaries([]Summary{{"a", strings.Repeat("x", 50)}, {"b", "y"}}, 10)
	if want := "- a: " + strings.Repeat("x", 50) + "\n… and 1 more files\n"; got != want {
		t.Errorf("FitSummaries = %q, want %q", got, want)
	}
}

func TestSystem(t *testing.T) {
	got := System([]string{"feat", "wip"}, ";")
	for _, want := range []string{"- feat: adds a new feature\n", "- wip\n", "no line that starts with `;`."} {
		if !strings.Contains(got, want) {
			t.Errorf("System() is missing %q", want)
		}
	}
	if strings.Contains(System([]string{"feat"}, ""), "no line that starts with") {
		t.Error("System() with no comment prefix still mentions one")
	}
}
