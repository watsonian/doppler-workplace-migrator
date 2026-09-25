package api

import (
	"strings"
	"testing"
)

// The migrator's address is written into every restricted config and compared
// there, never parsed back. A malformed one is silently useless: it reads as
// "no address matched" at the exact moment the run needs one to.
func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		in      string
		wantErr string
	}{
		{in: "203.0.113.47/32"},
		{in: "10.0.0.0/8"},
		{in: "2001:db8::/32"},
		{in: "203.0.113.47", wantErr: "needs a prefix length"},
		{in: "2001:db8::1", wantErr: "needs a prefix length"},
		{in: "not-an-address", wantErr: "not an address range"},
		{in: "203.0.113.47/33", wantErr: "not an address range"},
		{in: "", wantErr: "not an address range"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			err := validateCIDR(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCIDR(%q) = %v, want no error", tc.in, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateCIDR(%q) accepted it", tc.in)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCIDRSuggestsTheFixForABareAddress(t *testing.T) {
	// Typing an address without a prefix is the overwhelmingly likely mistake,
	// and the message should hand back the thing they meant.
	err := validateCIDR("203.0.113.47")
	if err == nil || !strings.Contains(err.Error(), "203.0.113.47/32") {
		t.Errorf("error should suggest the /32 form: %v", err)
	}
}
