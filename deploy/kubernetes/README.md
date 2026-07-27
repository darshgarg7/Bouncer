# Isolated sandbox deployment

These manifests are a security-oriented deployment template, not evidence that
the deployment has passed an independent assessment.

The worker runs with the `runsc` RuntimeClass, no service-account token, no
Linux capabilities, a read-only root filesystem, bounded writable volumes,
resource limits, and default-deny egress. The rooted backend uses Linux
`openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS`, and
`RESOLVE_NO_SYMLINKS`; it rejects hard-linked files and unrestricted commands.
Idempotency responses live on a persistent volume.

Before applying:

1. Install and validate gVisor on the target nodes.
2. Replace the image placeholder with a verified immutable digest.
3. Create `bouncer-sandbox-auth` from an external secret manager.
4. Terminate mutually authenticated TLS in a service mesh or gateway. The Go
   control plane intentionally rejects plaintext remote execution by default.
5. Confirm that the cluster's CNI enforces `NetworkPolicy` and that only the
   labeled control-plane pods can reach the service.
6. Run the Linux adversarial tests and a node-level teardown/residue audit.

The template intentionally exposes no shell or general network tool adapter.
