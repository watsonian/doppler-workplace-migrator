package main

import "testing"

func TestBrowsableURL(t *testing.T) {
	cases := []struct {
		name string
		host string
		port string
		want string
	}{
		{
			name: "carries the scheme, which is what makes it clickable",
			host: "127.0.0.1",
			port: "8080",
			want: "http://127.0.0.1:8080",
		},
		{
			// Docker binds the wildcard. Handing a browser http://0.0.0.0:8080
			// is a clickable dead end on macOS, so the printed host is the one
			// a person should actually open.
			name: "a wildcard bind is shown as localhost",
			host: "0.0.0.0",
			port: "8080",
			want: "http://localhost:8080",
		},
		{
			name: "the IPv6 wildcard too, in both spellings",
			host: "::",
			port: "8080",
			want: "http://localhost:8080",
		},
		{
			name: "bracketed IPv6 wildcard",
			host: "[::]",
			port: "8080",
			want: "http://localhost:8080",
		},
		{
			name: "an unset host is localhost rather than a bare colon",
			host: "",
			port: "8080",
			want: "http://localhost:8080",
		},
		{
			name: "a named host is left alone",
			host: "migrator.internal",
			port: "9000",
			want: "http://migrator.internal:9000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := browsableURL(tc.host, tc.port); got != tc.want {
				t.Errorf("browsableURL(%q, %q) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
		})
	}
}
