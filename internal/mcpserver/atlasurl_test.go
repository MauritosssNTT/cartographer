package mcpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/webui"
)

// routedAtlasHandler is newRoutedTestHandler with the Atlas surface set as
// serve sets it: web reports whether EnableWeb mounted a bundle, addr is the
// listen address the Atlas URL is derived from.
func routedAtlasHandler(t *testing.T, web bool, addr string, names ...string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	if web {
		multi.EnableWeb(http.NotFoundHandler())
	}
	multi.SetAtlasURL(webui.LoopbackURL(addr))
	return routedTokenStore(names...).Middleware(multi.Handler())
}

// #1945: kb_status names the Atlas deep link of the KB it reports on, only
// when the HTTP server serves the Atlas.
func TestKBStatus_UIURLOnlyWhenTheAtlasIsServed(t *testing.T) {
	t.Parallel()
	served := routedAtlasHandler(t, true, "0.0.0.0:39273", "kb-a", "work-kb")
	for _, name := range []string{"kb-a", "work-kb"} {
		obj, _ := routedKBStatusConnection(t, served, RoutedMountPath, name)
		if want := "http://127.0.0.1:39273/ui/?kb=" + name; obj["ui_url"] != want {
			t.Errorf("%s: ui_url = %v, want %s", name, obj["ui_url"], want)
		}
	}
	// web.enabled off: EnableWeb is never called, the address alone says nothing.
	off := routedAtlasHandler(t, false, "0.0.0.0:39273", "kb-a")
	if obj, _ := routedKBStatusConnection(t, off, RoutedMountPath, "kb-a"); obj["ui_url"] != nil {
		t.Errorf("web off: ui_url = %v, want absent", obj["ui_url"])
	}
}

// The bind address is not the caller's to learn: only a loopback or wildcard
// bind yields a ui_url, a specific other address yields none.
func TestKBStatus_UIURLOnlyForALoopbackBind(t *testing.T) {
	t.Parallel()
	cases := []struct{ addr, want string }{
		{"127.0.0.1:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"[::1]:39273", "http://[::1]:39273/ui/?kb=kb-a"},
		{"localhost:39273", "http://localhost:39273/ui/?kb=kb-a"},
		{":39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"0.0.0.0:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"[::]:39273", "http://127.0.0.1:39273/ui/?kb=kb-a"},
		{"10.0.0.5:39273", ""},
		{"[2001:db8::5]:39273", ""},
		{"server.example.com:39273", ""},
	}
	for _, c := range cases {
		h := routedAtlasHandler(t, true, c.addr, "kb-a")
		obj, _ := routedKBStatusConnection(t, h, RoutedMountPath, "kb-a")
		if c.want == "" {
			if _, ok := obj["ui_url"]; ok {
				t.Errorf("bind %s: ui_url = %v, want absent", c.addr, obj["ui_url"])
			}
			raw, _ := json.Marshal(obj)
			if host, _, _ := net.SplitHostPort(c.addr); strings.Contains(string(raw), host) {
				t.Errorf("bind %s: the answer names the bind address", c.addr)
			}
			continue
		}
		if obj["ui_url"] != c.want {
			t.Errorf("bind %s: ui_url = %v, want %s", c.addr, obj["ui_url"], c.want)
		}
	}
}

// Under stdio no HTTP server mounted the KB: no Atlas, no field.
func TestKBStatus_NoUIURLUnderStdio(t *testing.T) {
	t.Parallel()
	s := New("test")
	RegisterKBTools(s, setupTestKB(t), Deps{})
	var out map[string]any
	if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["ui_url"]; ok {
		t.Errorf("stdio: ui_url = %v, want absent", out["ui_url"])
	}
}

// The entry paragraph a provisioned connection receives names the Atlas and
// where its URL is, so the agent never says there is no graphical view.
func TestEntryInstructions_NameTheAtlas(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"Atlas", "`ui_url`", "`kb_status`"} {
		if !strings.Contains(entryInstructions, want) {
			t.Errorf("entryInstructions does not carry %q", want)
		}
	}
}
