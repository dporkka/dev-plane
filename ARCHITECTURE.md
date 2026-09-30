# Architecture

Dev Plane is an open, provider-neutral control plane for software-development agents and automation.

Its durable responsibility is the lifecycle around software changes:

```text
work request
  -> workspace
  -> execution
  -> evidence
  -> policy / approval
  -> artifact
  -> provenance
```

The project should remain useful to a team that uses none of the optional services, products, or infrastructure maintained by Dev Plane's authors.

## Core boundary rule

> Dev Plane core must not depend on an optional integration. A capability belongs in core only when its semantics are provider-neutral and useful without another project maintained by the Dev Plane authors.

Product- or vendor-specific behavior belongs behind an interface, adapter, protocol boundary, or outside this repository.

This rule applies equally to integrations created by maintainers and integrations contributed by third parties.

## Core owns

Dev Plane core may own provider-neutral semantics for:

- work/task/spec lifecycle;
- workspace and execution lifecycle;
- capability and security policy;
- budgets and approval gates;
- verification evidence such as tests, builds, scans, and reviews;
- artifact publication state;
- audit/provenance records;
- repository-owned development contracts;
- provider discovery and orchestration.

Core types should use problem-domain names such as `Workspace`, `Run`, `Evidence`, `Artifact`, `Provider`, and `Event`.

They should not use an integration brand as the generic domain concept.

## Extensions own

Adapters and providers may own:

- vendor API details;
- provider-specific authentication and configuration;
- hosted scheduler semantics;
- proprietary or product-specific metadata;
- model-vendor request/response translation;
- forge-specific branch and pull-request APIs;
- external context or memory schemas;
- notification transports;
- deployment-platform details.

An integration may be first-party and well supported without becoming a core dependency.

## Current extension maturity

Not every integration seam is equally mature. Treat the following status as architectural guidance, not a permanent compatibility promise.

| Area | Current state | Compatibility expectation |
| --- | --- | --- |
| Workspace runtime | Public `packages/runtimes.Provider` | Provider implementations should satisfy `packages/runtimes/contracttest` |
| Remote runtime transport | Public runner HTTP adapter | Keep semantics aligned with `runtimes.Provider` |
| Model routing | Interface exists inside the API application | Experimental; do not treat as a stable external plugin API yet |
| Forge operations | Adapters exist and are evolving | Experimental until a provider-neutral public contract is explicitly versioned |
| Lifecycle activity sinks | Public `packages/activity.Sink` | Initial provider-neutral contract; core execution must not depend on delivery |
| Secrets | Multiple provider concepts exist | Experimental public boundary |

When an experimental seam becomes stable, it should gain:

1. a provider-neutral public interface or protocol;
2. executable conformance tests;
3. a provider-authoring guide;
4. compatibility/versioning rules;
5. at least one implementation that is not specific to a maintainer-only service.

## Dependency direction

Preferred dependency direction:

```text
applications / orchestration
        |
        v
public provider contracts
        |
        v
provider-neutral domain types

adapter implementations ---> external systems
```

Avoid:

```text
core ---> optional branded adapter ---> external product
```

and avoid making one provider's configuration schema the generic Dev Plane model.

## Standalone requirement

A minimal installation should remain viable with:

- Dev Plane;
- Git;
- a local database where required;
- a local or configured runtime;
- one usable model path when an AI task actually needs a model.

NATS, Temporal, external model gateways, external context stores, hosted runtimes, and other integrations may improve scale or capability, but should not be required merely to understand, inspect, or extend the core system.

## Dogfooding rule

Maintainer projects are important adversarial integration tests. They are not the specification.

When a maintainer project needs a reusable feature:

1. describe the requirement without using the product's domain vocabulary;
2. determine whether the semantics are truly shared;
3. prefer an adapter when only the integration differs;
4. add a provider contract or protocol when multiple implementations should interoperate;
5. keep the product-specific policy in the product repository.

Duplication is preferable to a false generic abstraction.

## Provider acceptance rule

A new provider should normally be mergeable without changing the provider-neutral lifecycle.

If adding a provider requires new core behavior, reviewers should first determine whether:

- the core contract is genuinely missing a general capability; or
- the provider is leaking vendor-specific semantics into core.

General contract changes require tests that apply to existing and new implementations.

## Public API discipline

Public extension contracts should be small and versioned deliberately.

Do not expose internal implementation structures merely to make extension convenient. Prefer capability-oriented interfaces with explicit inputs, outputs, errors, lifecycle semantics, and conformance tests.

See:

- [Runtime Providers](docs/runtime-providers.md)
- [Contributing](CONTRIBUTING.md)
- [Maintainer Portfolio Integration Example](docs/platform-convergence.md)
