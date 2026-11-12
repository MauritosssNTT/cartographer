---
topic: skills-services-secrets
---

# D-new-agent-frontmatter-strict-yaml — An agent's frontmatter gets D212's strict-YAML refusal, and a held agent that does not parse is named

**Decision.** `contract.ValidateAgentFile` refuses an agent whose frontmatter
`gopkg.in/yaml.v3` cannot parse, with the parser's error and the advice to quote
the value, exactly as D212 does for a skill. `BuildManifest` names an agent that
arrived by git with such a frontmatter in the manifest `issues` (the agent stays in
the manifest). The block cut and the parse live in one function,
`contract.FrontmatterYAML`, which `ValidateFrontmatterYAML` and the approval gate
(`FrontmatterDeclaresCommands`) both call.

**Why.** The write channel read an agent with the lenient OKF reader (D8), which
takes `description: Use when a: b` as the author meant. The approval gate parses
the same block with yaml.v3, fails, and answers "needs approval" because it fails
closed. The agent was accepted at write and held at every sync, and nothing said
why: the author saw a request to approve an agent that declares no command (#1984).
D212 closed this for skills; agents never got it. Two parsers answering the same
question about one file will disagree, so the write check now uses the gate's own
extraction. The cost: an agent that was accepted before is now refused when
rewritten.

**Alternatives rejected.**

- *Make the gate lenient.* It fails closed on purpose: a block it cannot parse may
  hide a `hooks` key, and the clients that parse with yaml.v3 would skip the agent
  anyway.
- *Exclude the agent from the manifest, as D191 does for a skill.* A held agent is
  already not installed, and excluding it would prune an installed copy the
  moment its frontmatter breaks; it stays held and keeps what is installed.
- *Name the reason only in the sync line.* The author would still write a file no
  client can load; refusing at the boundary comes first, and the issue line covers
  what arrives by git.
- *Make `internal/okf` strict.* Rejected by D212 for the same reasons.

**Consequences.** The helper sits in `contract` because `ValidateAgentFile` does and
the provisioning package imports `contract`, not the reverse. A new reader that
decides something from an agent's or skill's frontmatter with yaml.v3 calls
`FrontmatterYAML` instead of cutting the block itself. The issue is printed with the
other manifest issues as a `warning:` line on `sync`; `status` lists it under "not in
the catalogue", which is not exact for an agent that stays in the manifest.
