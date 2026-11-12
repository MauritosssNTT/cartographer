package webui

import (
	"fmt"
	"net"
	"net/url"
)

// URL is the browser address of the Atlas served on a server http address
// ("127.0.0.1:39273", ":39273", "0.0.0.0:9090", "[::]:9090"). A bare port and
// a wildcard bind become 127.0.0.1: a server can listen on a wildcard, but a
// browser cannot open one. "" when addr is empty or unparseable. Shared by
// `cartographer service status` (the ui: line), the tray and kb_status, so
// the three print the same address.
func URL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(host, port), MountPath)
}

// KBURL is the Atlas URL that opens one KB (the UI's ?kb= view state). The
// name is only ever a query value, escaped here.
func KBURL(uiURL, kb string) string {
	return uiURL + "?" + url.Values{"kb": {kb}}.Encode()
}
