# Modularization Plan v2.1 — Final Closeout

This closeout records the final measured implementation state and validation
evidence for the modularization branch. It intentionally separates dependency
ownership success from the remaining upstream-owned fork surface.

## Measurement point

- Tested runtime implementation head: `b8ea306715f0e3156797f11ac8eb999255160b35`
- Official NewAPI comparison base/current upstream main: `bdef117505247769268b209665fb3ad7554c3da7`
- Generated manifest: `docs/architecture/modularization/current-manifest.csv`
- Manifest rows: **728**
- Unclassified/default `UPSTREAM_CORE` rows: **0**
- Concrete ChuanAPI-owned paths outside `modules/chuanapi/` and
  `web/src/extensions/chuanapi/`: **0**

Final ownership distribution at the tested implementation head:

| Ownership | Files | Production files |
| --- | ---: | ---: |
| ChuanAPI module | 126 | 107 |
| Generic module | 58 | 27 |
| Generic extension kernel | 33 | 17 |
| Composition root | 6 | 3 |
| Generic core enhancement | 93 | 85 |
| Product-core customization | 251 | 250 |
| Data compatibility | 13 | 11 |
| Test/documentation compatibility | 131 | 1 |
| Ops/deployment | 17 | 7 |

The remaining upstream-owned production core surface is **346 files**:

```text
generic core enhancement       85
product-core customization    250
data compatibility             11
---------------------------------
core production surface       346
```

Therefore the earlier aspirational `<120–150` core-file target is **not met**.
The refactor achieved concrete module ownership and dependency isolation, but it
did not turn the customized product into a thin fork. The 346-file surface must
remain an explicit upstream-conflict backlog.

## Dependency/ownership result

The implemented direction is now:

```text
NewAPI core -> generic extension / runtime ports <- concrete modules
```

In particular:

- Wallet funding/runtime, Wallet/referral HTTP, Affiliate policy/maintenance,
  and Waffo/Waffo-Pancake provider runtime are module-owned.
- Async image generation is owned by generic `modules/imagegen`.
- Wallet frontend and customized payment-settings frontend are owned by
  `web/src/extensions/chuanapi/` and consumed through generic accessors.
- Concrete frontend bootstrap wiring is isolated in `web/src/extensions/bootstrap.ts`;
  generic `web/src/extensions/index.ts` is an accessor surface rather than the
  concrete composition side-effect root.
- Billing orchestration remains generic; Wallet funding is injected through
  `moneyport` factory/capability contracts.
- Existing ChuanAPI API routes inherit host-owned middleware through
  `extension.RouteScopes`; Wallet/Payment adapters no longer reconstruct the
  `/api` global middleware chain.
- Optional payment catalogues register independently in a frozen
  `paymentport.Registry`; provider ids, method types, field keys and deterministic
  registration order are explicit composition contracts.
- Root ledger/affiliate/topup/subscription/OptionMap persistence and the payment
  option DTO/default shape remain explicit deferred data-compatibility boundaries.

## Validation

Release-candidate backend validation after the payment failure-classification
hardening on `b8ea306715f0e3156797f11ac8eb999255160b35`:

- Codex release-review overlay (`^TestFinalReview`) for Pancake retry and Wallet
  Reserve replay — **PASS** for both payment and wallet packages.
- `go test -count=1 -timeout 900s ./...` — **PASS**.
- `go vet ./...` — **PASS**.
- `go build ./...` — **PASS**.
- `relaykit` independent `GOWORK=off go build ./...` — **PASS**.
- Payment binding SQLite tests cover fresh migration, legacy upgrade, repeated
  migration, `trade_no` uniqueness, and top-up/subscription transaction rollback
  when binding persistence fails — **PASS**.
- Frontend typecheck — **PASS**.
- oxlint — **0 errors**, with the existing four `prefer-add-event-listener`
  warnings only.
- protected-header `format:check` — **PASS**.
- `copyright:check` — **PASS**.
- production Rsbuild — **PASS**.
- single-worker full Vitest — **171/171 test files and 1408/1408 tests PASS**.
- Initial JS referenced directly by `index.html`: **2,378,749 bytes raw / 687,887
  bytes gzip**, preserving the restored lazy-loading budget.
- The final backend-only payment classification plus manifest commits changed no
  files under `web/` from the frontend-validated source tree.
- The default parallel frontend run remained resource/timing-sensitive on this
  Windows runner (5-second UI-test timeouts/DOM races); the previously failing
  files passed in isolation and the complete single-worker suite passed without
  increasing test timeouts.
- The Windows HTTP/2 no-GetBody negative test was stabilized to assert the
  transport-independent invariant (request fails, no retry, one intact body)
  rather than Go transport error wording; the focused test passed **20/20** runs.
- Real SQLite production migration/CRUD compatibility was previously **PASS** on
  SQLite **3.50.4**. In addition, the new payment binding table now has dedicated
  SQLite migration/rollback/uniqueness coverage in the current full Go suite.

Database limitation for this run:

- `TEST_FIXED_MYSQL_DSN` — unset.
- `TEST_FIXED_POSTGRES_DSN` — unset.
- `TEST_MYSQL_DSN` — unset.
- `TEST_POSTGRES_DSN` — unset.

Accordingly this closeout **does not claim MySQL/PostgreSQL certification**.
The repository CI matrix still defines real MySQL 5.7 and PostgreSQL 9.6 gates;
those external jobs remain required before a strict production freeze.

## Upstream rehearsal

Immediately before closeout, `upstream-official/main` was fetched again and was
still exactly:

`bdef117505247769268b209665fb3ad7554c3da7`

It is an ancestor of the branch. A fresh
`git merge-tree --write-tree HEAD upstream-official/main` completed with exit code
0 and no conflicts. The exact synthetic tree id is intentionally not recorded so
this document does not become stale when documentation-only commits advance HEAD.

This is a clean current-upstream rehearsal, but it is **not evidence** that the
future target of `<=5` manual conflicts across dozens of new upstream commits has
been achieved, because official `main` did not advance during this refactor.
That KPI can only be measured when a meaningful new upstream range exists.

## Release posture

- No production deployment was performed.
- The code-level HOLD findings from the `f26e1e5bb` release review are closed in
  committed changes and promoted to repository regression tests.
- Production remains **HOLD** until the real MySQL/PostgreSQL compatibility matrix
  and environment-specific release rehearsal are completed.
- Legacy pending Waffo/Waffo-Pancake orders must be audited before rollout using
  `bin/audit_payment_binding_pending.sql`; missing bindings are never synthesized
  from a newly arriving webhook. See `payment-binding-rollout.md`.
- This branch should be reviewed as one committed range from the repaired
  baseline `5afacf7da479dcf23b450a0535fbfcd67f4a39fb` through the final closeout.

## Post-review P2 hardening

The unified review found no P0/P1 defects and identified two architecture-level
P2 risks. Both are closed in committed, independently validated changes:

1. `8d3a23ceb` (`refactor: inherit host api route scopes`) moves API middleware
   ownership back to the host. Route modules consume stable host-created scope
   handles, so future host security/audit middleware changes are inherited rather
   than manually mirrored.
2. `47a7bfa43` (`refactor: add frozen payment provider registry`) replaces the
   single payment catalogue slot with an explicit multi-provider registry. It
   freezes before request handling, preserves provider registration order, and
   rejects duplicate provider ids/method types/field keys. Waffo and Waffo
   Pancake now register independently while existing host `PayMethods` keep their
   historical precedence during response assembly.

The remaining release limitations are therefore external/strategic rather than
these two P2 code defects: real MySQL/PostgreSQL execution is still pending, the
346-file upstream-owned production core surface remains above the thin-fork
target, and future-conflict performance cannot be measured until official main
advances by a meaningful commit range.

## Release-review hardening after `f26e1e5bb`

The production review of `f26e1e5bb` identified additional failure-recovery and
frontend-gate issues. They are closed as follows:

1. `94e32653a` — transient Pancake binding lookup/write failures are explicitly
   retryable and return 5xx; permanent identity mismatches still ACK. Formal tests
   cover top-up/subscription and recovery with exactly-once top-up credit.
2. `bd93f1af2` — Wallet Funding restores debit receipt shares per persisted
   operation identity across PreConsume/Reserve/replay and fails closed if refund
   receipt coverage is insufficient.
3. `dc07f3994` — ChuanAPI frontend descriptors and lazy React page wrappers are
   separated so `react(only-export-components)` passes without sacrificing lazy
   loading; protected formatting is normalized.
4. `8a0be51ef` — remaining custom frontend copyright headers are repaired.
5. `380eccee7` — payment binding SQLite fresh/upgrade/repeat migration, uniqueness,
   and rollback scenarios are regression-tested.
6. `eb7d139e4` — rollout documentation and a read-only legacy pending-order audit
   SQL are added; automatic webhook-derived backfill is explicitly forbidden.
7. `b8ea30671` — temporary TopUp/Subscription lookup failures are retryable, while
   provider-order identity conflicts are explicit permanent errors rather than
   infinite-retry conditions.
