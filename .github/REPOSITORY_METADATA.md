# GitHub repository metadata

Use these values when the repository is created or connected:

- **Description:** Go control plane for deterministic authorization, bounded execution, and verifiable evidence around AI-agent tool calls.
- **Topics:** `ai-agents`, `ai-safety`, `authorization`, `golang`, `llm`,
  `policy-engine`, `sandbox`, `security`, `tool-calling`, `verifiable-logs`
- **Social preview:** `docs/assets/bouncer-social-preview.png`
- **Homepage:** link the hosted technical article or demo when available.

## Profile pin copy

**Bouncer** — Deterministic authorization and evidence for AI-agent tool calls.
Go runtime, Python evaluation, 100,000-case policy parity, a credential-free
demo, and intentionally narrow security claims.

## Public launch checklist

1. Create the public repository and add it as `origin`.
2. Push `main`, wait for quality, container, and security workflows to pass,
   and enable branch protection for the required checks.
3. Add the CI badge only after its owner/name URL resolves.
4. Upload `docs/assets/bouncer-social-preview.png` as the repository social
   preview and apply the description and topics above.
5. Run the demo and release gate from a fresh clone.
6. Create the signed `v0.1.0` tag only after the release workflow and generated
   artifact checks are ready.
7. Pin Bouncer on the author profile beside no more than five complementary
   projects so its systems/security focus is immediately legible.

The README intentionally omits a CI badge until the final repository owner/name
is known and the workflow has completed successfully. Add the badge only after
that URL resolves.
