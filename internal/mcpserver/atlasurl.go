package mcpserver

import "github.com/BeppeTemp/cartographer/internal/webui"

// atlasKBURL is kb_status's ui_url (#1945): the Atlas deep link to this
// server's KB, "" when the server that mounted it serves no Atlas (stdio,
// web.enabled off, no bundle). The caller of kb_status can read the KB it
// names, so the link reveals nothing it cannot see.
func (s *Server) atlasKBURL() string {
	if s == nil || s.atlasURL == nil {
		return ""
	}
	base := s.atlasURL()
	if base == "" {
		return ""
	}
	if name := s.PolicyKB(); name != "" {
		return webui.KBURL(base, name)
	}
	return base
}
