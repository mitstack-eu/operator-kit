package reach

import "testing"

func TestSafeProbeURL(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"it returns the URL that will be dialled", "https://app.example.test/health?x=1", "https://app.example.test/health?x=1"},
		{"loopback is left to the caller's address policy, since operator tests probe an httptest fixture", "http://127.0.0.1:8080/", "http://127.0.0.1:8080/"},
		{"a file URL is refused", "file:///etc/passwd", ""},
		{"a gopher URL is refused", "gopher://example.test/", ""},
		{"an ftp URL is refused", "ftp://example.test/", ""},
		{"a URL with no host is refused", "https:///health", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeProbeURL(tc.raw)

			if got != tc.want || (tc.want == "") != (err != nil) {
				t.Errorf("safeProbeURL(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
			}
		})
	}
}
