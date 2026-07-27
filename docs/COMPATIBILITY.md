# Compatibility and deprecation policy

Before v1.0, Bouncer may change command flags, schemas, manifests, and event
payloads between minor releases. Every breaking change must:

1. increment the affected `schema_version` or artifact version;
2. add a compatibility test for the previous supported version or fail with an
   explicit migration error;
3. document the migration in `CHANGELOG.md`; and
4. preserve the old decoder for at least one minor release when doing so does
   not weaken a security boundary.

Security fixes may remove unsafe behavior without a deprecation window. The
release notes must call out that exception.

Go 1.25+ and Python 3.11+ are the current supported toolchains. Linux is required
for rooted-executor qualification; macOS supports the virtual development path.
