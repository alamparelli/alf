package appsdk

import "testing"

func TestContext_Bool(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  bool
		want bool
	}{
		{"true bool", map[string]any{"k": true}, "k", false, true},
		{"false bool", map[string]any{"k": false}, "k", true, false},
		{"string true", map[string]any{"k": "true"}, "k", false, true},
		{"string false", map[string]any{"k": "false"}, "k", true, false},
		{"string yes", map[string]any{"k": "yes"}, "k", false, true},
		{"string no", map[string]any{"k": "no"}, "k", true, false},
		{"string 1", map[string]any{"k": "1"}, "k", false, true},
		{"string 0", map[string]any{"k": "0"}, "k", true, false},
		{"float64 nonzero", map[string]any{"k": 3.14}, "k", false, true},
		{"float64 zero", map[string]any{"k": 0.0}, "k", true, false},
		{"missing key", map[string]any{}, "k", true, true},
		{"wrong type", map[string]any{"k": []any{}}, "k", true, true},
		{"invalid string", map[string]any{"k": "maybe"}, "k", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{Args: tt.args}
			if got := c.Bool(tt.key, tt.def); got != tt.want {
				t.Errorf("Bool(%q, %v) = %v, want %v", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestContext_Float64(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		key  string
		def  float64
		want float64
	}{
		{"float64", map[string]any{"k": 3.14}, "k", 0, 3.14},
		{"string float", map[string]any{"k": "2.718"}, "k", 0, 2.718},
		{"missing", map[string]any{}, "k", 9.9, 9.9},
		{"invalid string", map[string]any{"k": "abc"}, "k", 1.0, 1.0},
		{"wrong type", map[string]any{"k": true}, "k", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{Args: tt.args}
			got := c.Float64(tt.key, tt.def)
			if got != tt.want {
				t.Errorf("Float64(%q, %v) = %v, want %v", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestContext_StringSlice(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		key  string
		want []string
	}{
		{"valid slice", map[string]any{"k": []any{"a", "b", "c"}}, "k", []string{"a", "b", "c"}},
		{"empty slice", map[string]any{"k": []any{}}, "k", []string{}},
		{"missing", map[string]any{}, "k", nil},
		{"not a slice", map[string]any{"k": "hello"}, "k", nil},
		{"mixed types", map[string]any{"k": []any{"a", 42, "b"}}, "k", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{Args: tt.args}
			got := c.StringSlice(tt.key)
			if tt.want == nil {
				if got != nil {
					t.Errorf("StringSlice(%q) = %v, want nil", tt.key, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("StringSlice(%q) len = %d, want %d", tt.key, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("StringSlice(%q)[%d] = %q, want %q", tt.key, i, got[i], tt.want[i])
				}
			}
		})
	}
}
