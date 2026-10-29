package preview

import "testing"

func TestViewerURL(t *testing.T) {
	cases := []struct {
		public, path, want string
		ok                 bool
	}{
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "/app?x=1", "https://calm-river-12.trycloudflare.com/app?__lody_preview_token=abc&x=1", true},
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "", "https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", true},
		{"https://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "//evil.example/", "", false},
		{"https://example.com/?__lody_preview_token=abc", "/", "", false},
		{"http://calm-river-12.trycloudflare.com/?__lody_preview_token=abc", "/", "", false},
		{"https://calm-river-12.trycloudflare.com:8443/?__lody_preview_token=abc", "/", "", false},
		{"https://calm-river-12.trycloudflare.com/", "/", "", false},
	}
	for _, c := range cases {
		got, ok := ViewerURL(c.public, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("ViewerURL(%q, %q) = %q, %v; want %q, %v", c.public, c.path, got, ok, c.want, c.ok)
		}
	}
}
