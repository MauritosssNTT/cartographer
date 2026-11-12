package recipes_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/startest"
)

// privateCall lists through a recipe that GETs path on host; every name resolves
// to the loopback test server, as an internal wiki resolves to a private address.
func privateCall(t *testing.T, srv *httptest.Server, host, path string, perms *core.Permissions, grant []string) error {
	t.Helper()
	port := srv.URL[strings.LastIndexByte(srv.URL, ':')+1:]
	p := load(t, fmt.Sprintf(`
def lst(r):
    resp = http.request("GET", "https://%s:%s%s")
    return {"entries": [{"key": "k", "title": "t", "revision": "1", "change": "added", "content": resp["body"]}], "next_cursor": "c"}
recipe("wiki", lst)
`, host, port, path))
	tlsCfg := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsCfg.ServerName = "example.com" // the test certificate's name; the host checks are on the URL
	req := recipes.Request{Recipe: "wiki", Verb: "list", Body: []byte(`{}`), Plugin: "acme",
		Permissions: perms, PrivateHosts: grant,
		Seams: &startest.Seams{Net: &startest.NetSeams{
			TLS: tlsCfg,
			Lookup: func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			},
		}}}
	_, err := p.Call(context.Background(), req)
	return err
}

func denied(t *testing.T, err error, want string) {
	t.Helper()
	var ce *starcore.CallError
	if !errors.As(err, &ce) || ce.Kind != starcore.Denied || !strings.Contains(ce.Msg, want) {
		t.Errorf("want denied with %q, got %v", want, err)
	}
}

func TestPrivateGrantNeedsBothKeys(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "https://other.example.com"+r.Host[strings.LastIndexByte(r.Host, ':'):]+"/", http.StatusFound)
			return
		}
		fmt.Fprint(w, "page")
	}))
	defer srv.Close()
	declared := &core.Permissions{Net: []string{"*.example.com"}, NetPrivate: []string{"wiki.example.com", "other.example.com"}}

	// Both keys: the manifest declares it, the Surveyor grants it.
	if err := privateCall(t, srv, "wiki.example.com", "/", declared, []string{"wiki.example.com"}); err != nil {
		t.Fatalf("granted host: %v", err)
	}
	// Declared, not granted: the refusal names the missing grant.
	denied(t, privateCall(t, srv, "wiki.example.com", "/", declared, nil), "plugins.acme.allow_private_hosts")
	// Granted, not declared: the host's list alone opens nothing.
	undeclared := &core.Permissions{Net: []string{"*.example.com"}}
	denied(t, privateCall(t, srv, "wiki.example.com", "/", undeclared, []string{"wiki.example.com"}), "may not reach")
	// Another host under the same net pattern keeps the refusal.
	denied(t, privateCall(t, srv, "api.example.com", "/", declared, []string{"wiki.example.com"}), "may not reach")
	// A redirect from the granted host to a private host without the grant is refused.
	denied(t, privateCall(t, srv, "wiki.example.com", "/hop", declared, []string{"wiki.example.com"}), "other.example.com")
	// The net allow-list runs first: a granted name outside net is refused by it.
	denied(t, privateCall(t, srv, "wiki.example.com", "/", &core.Permissions{Net: []string{"api.example.org"}, NetPrivate: []string{"wiki.example.com"}}, []string{"wiki.example.com"}), "permissions.net")
}

// The grant opens private, loopback and link-local unicast addresses only.
func TestPrivateGrantKeepsTheOtherRangesShut(t *testing.T) {
	for _, ip := range []string{"224.0.0.1", "0.0.0.0", "100.64.0.1", "198.18.0.1"} {
		p := load(t, `
def lst(r):
    http.request("GET", "https://wiki.example.com/")
    return {"next_cursor": ""}
recipe("wiki", lst)
`)
		addr := netip.MustParseAddr(ip)
		_, err := p.Call(context.Background(), recipes.Request{Recipe: "wiki", Verb: "list", Body: []byte(`{}`), Plugin: "acme",
			Permissions:  &core.Permissions{Net: []string{"wiki.example.com"}, NetPrivate: []string{"wiki.example.com"}},
			PrivateHosts: []string{"wiki.example.com"},
			Seams: &startest.Seams{Net: &startest.NetSeams{
				Lookup: func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{addr}, nil },
				Proxy:  func(*http.Request) (*url.URL, error) { return nil, nil },
			}}})
		denied(t, err, "the private grant does not open")
	}
}
