package message

import "testing"

var types = []string{"feat", "fix", "docs"}

func TestValidate(t *testing.T) {
	tests := []struct {
		msg   string
		valid bool
	}{
		{"feat: add x", true},
		{"feat(auth): add x", true},
		{"feat(auth)!: add x", true},
		{"feat!: add x", true},
		{"FIX: add x", true}, // types are case-insensitive
		{"feat add x", false},
		{"feat:add x", false},
		{"feat:  add x", false},
		{"feat: ", false},
		{"feat():x", false},
		{"feat(): x", false},
		{"feat(a(b)): x", false},
		{"feat(a: x", false},
		{"feat(a)b: x", false},
		{"wip: x", false},
		{"", false},
		{"feat: x\n\n## Changes\n- y", false},
		{"feat: x\n\nRefs: #12", true},
	}
	for _, tt := range tests {
		if err := Validate(tt.msg, types, "#"); (err == nil) != tt.valid {
			t.Errorf("Validate(%q) = %v, want valid=%v", tt.msg, err, tt.valid)
		}
	}
}

func TestValidateCommentPrefix(t *testing.T) {
	const msg = "feat: x\n\n# kept\n;; dropped"
	tests := []struct {
		prefix string
		valid  bool
	}{
		{";;", false},
		{"//", true},
		{"", true}, // git strips nothing
	}
	for _, tt := range tests {
		if err := Validate(msg, types, tt.prefix); (err == nil) != tt.valid {
			t.Errorf("Validate with prefix %q = %v, want valid=%v", tt.prefix, err, tt.valid)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"FEAT(Auth): X", "feat(Auth): X"},
		{"Fix!: x", "fix!: x"},
		{"feat: x\nbody", "feat: x\n\nbody"},
		{"feat: x\n\nbody", "feat: x\n\nbody"},
		{"  feat: x\r\n\r\nbody\r\n", "feat: x\n\nbody"},
		{"feat: x\n\nbreaking change: y", "feat: x\n\nBREAKING CHANGE: y"},
		{"feat: x\n\nBreaking-Change: y", "feat: x\n\nBREAKING-CHANGE: y"},
		{"feat: x\n\nbody\n\nbreaking change: y", "feat: x\n\nbody\n\nBREAKING CHANGE: y"},
		{"feat: x\n\nbreaking change in body text", "feat: x\n\nbreaking change in body text"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
