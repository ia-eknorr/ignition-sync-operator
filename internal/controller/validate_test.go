package controller

import (
	"strings"
	"testing"
)

func TestValidateVarKeys(t *testing.T) {
	cases := []struct {
		name    string
		vars    map[string]string
		wantErr string // substring; empty means no error expected
	}{
		{
			name: "valid simple keys",
			vars: map[string]string{"projectName": "demo", "siteNumber": "1", "_private": "x"},
		},
		{
			name:    "key with dash",
			vars:    map[string]string{"my-var": "value"},
			wantErr: "my-var",
		},
		{
			name:    "key with dot",
			vars:    map[string]string{"my.var": "value"},
			wantErr: "my.var",
		},
		{
			name:    "key starts with digit",
			vars:    map[string]string{"1var": "value"},
			wantErr: "1var",
		},
		{
			name: "nil map is valid",
			vars: nil,
		},
		{
			name: "empty map is valid",
			vars: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVarKeys(tc.vars, "test.vars")
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
