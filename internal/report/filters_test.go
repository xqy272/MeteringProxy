package report

import "testing"

func TestValidateKeyHashFilter(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, value := range []string{"", "unknown", valid} {
		if err := ValidateKeyHashFilter(value); err != nil {
			t.Fatalf("ValidateKeyHashFilter(%q): %v", value, err)
		}
	}
	for _, value := range []string{
		"0123456789abcdef",
		" " + valid,
		valid + " ",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
		"g123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if err := ValidateKeyHashFilter(value); err == nil {
			t.Fatalf("ValidateKeyHashFilter(%q) succeeded, want rejection", value)
		}
	}
}
