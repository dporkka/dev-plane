# Run Manifest v1

A Run Manifest is the immutable admission record for one Dev Plane execution.

It binds the control-plane decision to the exact execution request before an
agent is allowed to mutate a repository or runtime. The manifest deliberately
contains only provider-neutral facts:

- run, task, repository, and immutable base commit identity;
- agent role and execution class;
- selected runtime provider;
- CPU, memory, disk, and wall-time ceilings;
- granted runtime authority: network, secret names, and allowed operations;
- model/tool/runtime budget ceilings;
- required verification gates and acceptance criteria.

## Digest invariant

The manifest digest is SHA-256 over canonical JSON that excludes the digest
field itself. Set-like fields (operations, secrets, and required gates) are
trimmed, deduplicated, and sorted before hashing. Acceptance criteria retain
their declared order.

Any mutation after admission must therefore fail:

```text
manifest.VerifyDigest() == nil
```

A changed resource ceiling, authority grant, repository SHA, role, budget, or
gate changes the digest.

## Why it is not wired into task approval yet

The current approval path provisions a workspace from a repository branch
name. It does not yet resolve and persist the immutable base commit SHA before
runtime creation.

A manifest that hashes only `main` or another mutable ref would provide
misleading provenance. The next integration step is therefore:

1. resolve the approved repository ref to an immutable commit SHA;
2. allocate the run ID before workspace creation;
3. construct and persist the Run Manifest;
4. attach its digest to the runtime create request, approvals, snapshots,
   verification evidence, and PR provenance;
5. require the same digest when resuming or retrying the run.

The runtime provider may enforce a subset of the manifest physically, but it
must never expand authority beyond the admitted manifest.
