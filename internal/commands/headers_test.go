package commands

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		input []string
		want  map[string]string
	}{
		{[]string{"Authorization: Bearer token"}, map[string]string{"Authorization": "Bearer token"}},
		{[]string{"Authorization: Bearer to.ke.n"}, map[string]string{"Authorization": "Bearer to.ke.n"}},
		{[]string{"Key:Value"}, map[string]string{"Key": "Value"}},
		{[]string{"Key:   Value with spaces"}, map[string]string{"Key": "Value with spaces"}},
		{[]string{"NoColonHere"}, map[string]string{}},
		{[]string{"JustKey:"}, map[string]string{"JustKey": ""}},
		{[]string{":JustValue"}, map[string]string{"": "JustValue"}},
		{[]string{"A:1", "B:2"}, map[string]string{"A": "1", "B": "2"}},
		{[]string{"A:1", "NoColon", "B:2"}, map[string]string{"A": "1", "B": "2"}},
	}

	logger := zerolog.Nop()

	for _, tt := range tests {
		got := ParseHeaders(tt.input, logger)
		if len(got) != len(tt.want) {
			t.Errorf("parseHeaders(%v) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("parseHeaders(%v)[%q] = %q, want %q", tt.input, k, got[k], v)
			}
		}
	}
}
