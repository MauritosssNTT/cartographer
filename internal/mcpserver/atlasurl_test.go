package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/webui"
)

// routedAtlasHandler is newRoutedTestHandler with the Atlas surface set as
// serve sets it: web reports whether EnableWeb mounted a bundle.
func routedAtlasHandler(t *testing.T, web bool, names ...string) http.Handler {
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
	multi.SetAtlasURL(webui.URL("0.0.0.0:39273"))
	return routedTokenStore(names...).Middleware(multi.Handler())
}

// #1945: kb_status names the Atlas deep link of the KB it reports on, only
// when the HTTP server serves the Atlas.
func TestKBStatus_UIURLOnlyWhenTheAtlasIsServed(t *testing.T) {
	t.Parallel()
	served := routedAtlasHandler(t, true, "kb-a", "work-kb")
	for _, name := range []string{"kb-a", "work-kb"} {
		obj, _ := routedKBStatusConnection(t, served, RoutedMountPath, name)
		if want := "http://127.0.0.1:39273/ui/?kb=" + name; obj["ui_url"] != want {
			t.Errorf("%s: ui_url = %v, want %s", name, obj["ui_url"], want)
		}
	}
	// web.enabled off: EnableWeb is never called, the address alone says nothing.
	off := routedAtlasHandler(t, false, "kb-a")
	if obj, _ := routedKBStatusConnection(t, off, RoutedMountPath, "kb-a"); obj["ui_url"] != nil {
		t.Errorf("web off: ui_url = %v, want absent", obj["ui_url"])
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
