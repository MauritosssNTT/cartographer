---
topic: sync-provisioning
---

# D-new-no-paths-table-in-tracked-files — A tracked instructions file gets the placeholder paragraph, not the "Local paths" table

**Decision.** A project-scope instructions block whose destination file git tracks — the
file itself, or the file inside the workspace a symlink at the destination resolves to —
carries the placeholder paragraph ([D262](D262-path-placeholders-are-visible-resolved-and-onboarded.md))
and no "Local paths" rows. The client decides it once per projection
(`provisioning.InstructionsDestTracked`, handed to `Apply` and to the status diff as
`ApplyOptions.OmitPathsTable`), so the paths-section hash that sync records is the one status
recomputes. An untracked or client-owned destination (`CLAUDE.local.md`, a kiro steering file,
an untracked `AGENTS.md`) and the global scope keep the table; where git cannot say
(no repository, git missing, an unreadable index) the table stays too, as before.

**Why.** The table is absolute paths of one machine, usually containing the OS user name
([D259](D259-published-text-carries-no-private-names.md): a real setup does not belong in a shared
file). An explicit `workspace bind` ([D193](D193-workspace-scoped-artifact-projection-one-provider.md))
writes the block into the project's `CLAUDE.md`/`AGENTS.md`, which a repository often versions,
and through a `CLAUDE.md -> AGENTS.md` link into the tracked target: every developer who syncs
rewrote a committed file with their own paths, and the diff never settled (#1897). The paragraph
is the same text for everyone and already tells the agent to run `cartographer resolve`, so the
agent loses a cache, not a capability. What it costs: in a tracked file the agent has to resolve
each key on demand, and a file that becomes tracked after a first sync is rewritten once, without
the rows.

**Alternatives rejected.**
- *Leave the table and warn.* The paths are on the remote as soon as someone commits; a warning
  after the write is too late.
- *Write the block to a local file for every client.* Codex and Copilot have no untracked
  instructions file the client reads beside the tracked one; a different file per client is a
  separate decision about where an explicit bind writes, not this one.
- *Omit the table everywhere.* An untracked or client-owned file is the one place the machine's
  paths are safe and useful.
- *Judge the file in `sync` only.* `status` rebuilds the hash without the sync's inputs; a flag only
  one side knew reported drift forever (the `TestShared_Convergence` failure mode).

**Consequences.** Not decided here: whether an explicit bind should write into a tracked file at
all, or offer a bind without the block ([D193](D193-workspace-scoped-artifact-projection-one-provider.md)
stands; a derived binding still never edits a tracked file, [D1019](D1019-a-repository-a-kb-declares-is-bound-to-it.md)).
The instructions text itself still has its placeholders expanded, so a KB instruction that cites
a key writes that path into the block; only the table is withheld. A new client-written part of
the block that depends on the machine needs the same answer for a tracked file. `doctor`'s
symlink finding names the tracked target and offers replacing the link before listing it.
