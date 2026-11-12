---
topic: control-plane
---

# D-new-atlas-url-in-status — kb_status names the Atlas deep link of its KB, only where the Atlas is served

**Decision.** `kb_status` carries `ui_url`, the Atlas deep link to the KB it reports on (`webui.KBURL` of `webui.URL(<listen address>)`, `?kb=<mount name>`), only when the serving process mounted the Atlas bundle (HTTP with `web.enabled` on); under stdio and with the UI off the field is absent. The provisioned entry paragraph gains one sentence saying the Atlas is the browser view of the KB and its URL is `ui_url` in `kb_status`; `cartographer-ops` says where it is and why it may be missing. `webui.URL` and `webui.KBURL` are the one derivation `cartographer service status` (the `ui:` line), the tray and `kb_status` share: a bare port or a wildcard bind (`0.0.0.0`, `::`) becomes `127.0.0.1`.

**Why.** An agent asked "can I see my KB?" answered that no graphical view exists while the Atlas was serving (#1945): nothing on the MCP surface named it. The address is the listen address the server was started with, read at request time, so it needs no network call (D151's rule for `kb_status`) and costs nothing on stdio. The cost: behind a reverse proxy or a non-loopback host the printed address is the bind, not the public URL; that is what `service status` already prints, and a deployment behind a proxy knows its own address.

**Alternatives rejected.** Importing `tray.KBURL` from `mcpserver`: the tray links purego and the service manager, a GUI dependency a server package has no use for, so the helper moved to `internal/webui`, which both already import. The base URL alone, without the deep link: the call names one KB, which the caller can read, so the link reveals nothing more and saves a click. The sentence also in the generic guide (`initialize` without a block, `kb_guide`): over the guide's byte budget for a second copy of one sentence; the field is self-describing in `kb_status` itself. A `ui_url` on `/health`: it is unauthenticated, and the tray already builds the address from the config.

**Consequences.** A new place that prints the Atlas address calls `webui.URL`, never a copy of the wildcard rewrite. `ui_url` stays absent, never empty, when no Atlas is served, so its presence is the answer to "is there a UI here".
