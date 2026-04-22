# Security policy

## Supported versions

| Version | Status      |
|---------|-------------|
| 0.1.x   | Supported   |

Older pre-1.0 releases are not supported. Security fixes for 0.1.x ship as
patch releases (`0.1.1`, `0.1.2`, …) and are announced on the GitHub release
page.

## Reporting a vulnerability

Please report security issues privately rather than in a public GitHub issue.

Send a report to: skyforce77@users.noreply.github.com

Include in your report:

- A description of the vulnerability and its impact.
- Steps to reproduce (ideally a minimal Go snippet or failing test).
- The commit SHA or release version that is affected.
- Any mitigations you have already identified.

We aim to acknowledge receipt within 72 hours and to share a remediation plan
within 7 days. Expect a coordinated disclosure window of up to 90 days for
non-critical issues; actively exploited vulnerabilities are disclosed faster.

## Scope

In scope:

- `pkg/` libraries and their public API.
- `cmd/tinyagentsctl` behavior.
- Default wire format in `internal/wire` and `internal/proto`.

Out of scope:

- Issues in third-party LLM providers, gossip libraries, or Go standard
  library — report those upstream.
- Operational misconfiguration (running untrusted nodes on a public network,
  missing TLS at the operator layer, etc.) — see `docs/clustering.md` for
  the supported threat model.

## Known advisories

tinyagents 0.1.x pulls `github.com/ollama/ollama@v0.5.12` for its Ollama
adapter. `govulncheck` flags nine advisories in that module (GO-2025-3548
family). Newer ollama releases change the Go SDK surface in ways that are
not yet adapted in `pkg/llm/ollama`. A pinned upgrade is tracked as a
roadmap item for 0.2; in the meantime, operators who treat Ollama endpoints
as trusted (the recommended deployment) are not at additional risk from
these advisories.

CI runs `govulncheck` on every push and surfaces findings without gating
releases, so new advisories are visible in workflow logs.

## Safe harbor

Research conducted in good faith against tinyagents — following this policy,
avoiding data exfiltration, and limiting disruption to running services — is
welcome. We will not pursue legal action against researchers who comply.
