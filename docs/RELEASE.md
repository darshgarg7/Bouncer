# Release and rollout checklist

## Build evidence

1. Run `make check` from a clean checkout.
2. Run `make evaluate-synthetic`, `make evaluate-ablation`, `make evaluate-projector`, `make evaluate-mechanisms`, and the OPE known-ground-truth validation.
3. Review generated reports and confirm that fixture, manifest, and schema changes were intentional.
4. Build both container images with immutable version tags.
5. Record source revision, Go version, Python version, model ID, provider/NIM version, and image digests.
6. Run `bouncer-verify-log` for every included run and record each terminal hash.
7. Run Linux rooted-executor adversarial tests on the release kernel/runtime; macOS compilation is insufficient.

## Secrets and configuration

- Store `NIM_API_KEY`, `NGC_API_KEY`, and `BOUNCER_SANDBOX_TOKEN` in a secret manager.
- Never place credentials in manifests, images, task fixtures, or event logs.
- Use a unique sandbox token per environment.
- Terminate TLS at the sandbox workload or a mutually authenticated sidecar.

## Rollout order

1. Shadow: proposal and routing only; no execution.
2. Virtual: execute against copied state and compare with expected outcomes.
3. Approval: require a human decision for every mutating action.
4. Canary: enable reversible operations for a narrowly defined task class.
5. Expand only while pass rate, severe mutations, truncation, cost, and p95 latency remain inside frozen gates.

## Rollback triggers

- unauthorized or unattributed state mutation;
- missing or corrupt event sequence;
- sandbox authentication or TLS bypass;
- repeated idempotency-key mismatch;
- pass-rate non-inferiority breach;
- unexplained token or latency regression; or
- provider response-contract drift.

Rollback disables remote execution first. Proposal and shadow logging may remain active for diagnosis.
