package webui

import "testing"

func TestURL(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		":39273":            "http://127.0.0.1:39273/ui/",
		"127.0.0.1:39273":   "http://127.0.0.1:39273/ui/",
		"0.0.0.0:9090":      "http://127.0.0.1:9090/ui/",
		"[::]:9090":         "http://127.0.0.1:9090/ui/",
		"10.0.0.5:9090":     "http://10.0.0.5:9090/ui/",
		"not-a-valid-value": "",
	}
	for addr, want := range cases {
		if got := URL(addr); got != want {
			t.Errorf("URL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestKBURL(t *testing.T) {
	if got, want := KBURL("http://127.0.0.1:39273/ui/", "kb a&b"), "http://127.0.0.1:39273/ui/?kb=kb+a%26b"; got != want {
		t.Errorf("KBURL = %q, want %q", got, want)
	}
}
