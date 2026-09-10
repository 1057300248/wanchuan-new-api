# Dependency Boundaries

## Backend

The target direction is `core -> port <- module`. Core production code must not
import `chuanapi` or `modules/chuanapi`. The one production exception is
`main.go`, which is the application composition root and may invoke
`chuanapi.Register`.

`chuanapi/register.go` is a temporary composition adapter. It may depend on host
and module packages while the migration is in progress, but its final state must
only compose independently owned modules. Root `model`, `service`, `controller`,
`router` and `relay` packages must never import it.

## Extension kernel versus runtime ports

These are deliberately separate mechanisms:

- `extension/` is startup contribution infrastructure. Its current external
  dependency whitelist remains **stdlib + gin + gorm only**. Batch A does not
  permit a generic DTO dependency and does not relax `extension/boundary_test.go`.
- `pkg/moneyport/` and future `pkg/*port` packages define runtime capability
  contracts and their own host-independent DTOs. They do not become extension
  descriptors or service locators.

If a future extension descriptor genuinely needs a shared DTO package, that is
an architecture change requiring a separate ADR and an explicit boundary-test
update. It must not happen as incidental refactoring.

## Frontend

`web/src/extensions/index.ts` is the frontend composition root. Concrete
ChuanAPI implementation lives under `web/src/extensions/chuanapi/`.

At the Batch A baseline, three route shells still directly import
`@/extensions/chuanapi/help`. They are a measured migration debt, not a permanent
exception. Batch A allows only those three existing files and forbids any new
concrete import. Phase 2 replaces them with a generic help/page contribution;
after that the gate changes to zero.

## Brand literals

Batch A freezes the three current production-core files containing
`chuanapi.com` string literals. Phase 1 will move those values behind trusted
configuration/composition. New production-core literals are forbidden now.
