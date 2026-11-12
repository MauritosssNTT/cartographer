package tray

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// A program that is not a Cartographer server on the server address is never
// Running: the icon is Stopped and the status line names the address (#1998).
func TestProbeHealthForeignListener(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name    string
		addr    string
		foreign bool
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"1.2.3","ready":true}`))
		}), false},
		{"ours, bootstrapping", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"1.2.3","bootstrapping":true}`))
		}), false},
		{"a 200 that says ok", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), true},
		{"a 200 that is not json", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`hello`)) }), true},
		{"a 404", serve(http.NotFound), true},
		{"nothing", "127.0.0.1:1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := probeHealth(tc.addr)
			if got := errors.Is(err, ErrForeignListener); got != tc.foreign {
				t.Fatalf("foreign = %v, want %v (err %v)", got, tc.foreign, err)
			}
			if tc.foreign && h != nil {
				t.Errorf("a foreign answer returned a Health: %+v", h)
			}
			snap := Classify(h, err, service.Status{Installed: true, HTTPAddr: tc.addr})
			if tc.foreign {
				if snap.State != StateStopped || snap.Foreign != tc.addr {
					t.Errorf("snapshot = %+v, want Stopped with Foreign=%s", snap, tc.addr)
				}
			} else if snap.Foreign != "" {
				t.Errorf("Foreign = %q on a server that is not foreign", snap.Foreign)
			}
		})
	}
}

func TestClassifyForeignIsStoppedEvenWhenNothingIsInstalled(t *testing.T) {
	snap := Classify(nil, ErrForeignListener, service.Status{HTTPAddr: "127.0.0.1:39273"})
	if snap.State != StateStopped || snap.Foreign != "127.0.0.1:39273" {
		t.Errorf("snapshot = %+v", snap)
	}
	if got := StatusLine(snap); got != "another program answers on 127.0.0.1:39273" {
		t.Errorf("StatusLine = %q", got)
	}
	if head := InfoItems(snap)[0]; head.Badge != "Port in use" || head.Tone != ToneDown {
		t.Errorf("head = %+v", head)
	}
	plain := Classify(nil, errors.New("refused"), service.Status{Installed: true})
	if plain.Foreign != "" || StatusLine(plain) != "stopped" {
		t.Errorf("a plain stop = %+v / %q", plain, StatusLine(plain))
	}
}

func TestForeignCheckLeavesOtherErrorsAlone(t *testing.T) {
	starting := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeStarting}
	if _, err := foreignCheck(nil, starting); errors.Is(err, ErrForeignListener) || err != error(starting) {
		t.Errorf("a bootstrapping server became %v", err)
	}
	refused := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnreachable}
	if _, err := foreignCheck(nil, refused); errors.Is(err, ErrForeignListener) {
		t.Error("a refused connection read as foreign")
	}
}
