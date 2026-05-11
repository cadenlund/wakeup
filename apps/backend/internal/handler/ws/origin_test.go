package ws

import (
	"reflect"
	"testing"
)

func TestOriginHostPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, nil},
		{"blanks dropped", []string{"", "  "}, nil},
		{
			"strips scheme",
			[]string{"http://localhost:8081"},
			[]string{"localhost:8081"},
		},
		{
			"https + custom scheme",
			[]string{"https://app.wakeup.app", "exp://localhost:8081"},
			[]string{"app.wakeup.app", "localhost:8081"},
		},
		{"wildcard passthrough", []string{"*"}, []string{"*"}},
		{"bare host passthrough", []string{"localhost:8081"}, []string{"localhost:8081"}},
		{
			"mixed + trims",
			[]string{" http://localhost:8081 ", "*", "example.com"},
			[]string{"localhost:8081", "*", "example.com"},
		},
		{
			"unparseable kept verbatim",
			[]string{"://nope"},
			[]string{"://nope"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := originHostPatterns(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("originHostPatterns(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
