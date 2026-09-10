# Modularization Plan v2.1 — Final Closeout

This closeout records the final measured implementation state and validation
evidence for the modularization branch. It intentionally separates dependency
ownership success from the remaining upstream-owned fork surface.

## Measurement point

- Tested implementation head: `b0ec178b4010e769bd04a96e266cbc37d6e2c1f7`
- Official NewAPI comparison base/current upstream main: `bdef117505247769268b209665fb3ad7554c3da7`
- Generated manifest: `docs/architecture/modularization/current-manifest.csv`
- Manifest rows: **710**
- Unclassified/default `UPSTREAM_CORE` rows: **0**
- Concrete ChuanAPI-owned paths outside `modules/chuanapi/` and
  `web/src/extensions/chuanapi/`: **0**

Final ownership distribution at the tested implementation head:

| Ownership | Files | Production files |
| --- | ---: | ---: |
| ChuanAPI module | 118 | 105 |
| Generic module | 58 | 27 |
| Generic extension kernel | 32 | 17 |
| Composition root | 6 | 3 |
| Generic core enhancement | 93 | 85 |
| Product-core customization | 249 | 248 |
| Data compatibility | 12 | 10 |
| Test/documentation compatibility | 126 | 1 |
| Ops/deployment | 16 | 7 |

The remaining upstream-owned production core surface is **343 files**:

```text
generic core enhancement       85
product-core customization    248
data compatibility             10
---------------------------------
core production surface       343
```

Therefore the earlier aspirational `<120–150` core-file target is **not met**.
The refactor achieved concrete module ownership and dependency isolation, but it
did not turn the customized product into a thin fork. The 343-file surface must
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
- Root ledger/affiliate/topup/subscription/OptionMap persistence and the payment
  option DTO/default shape remain explicit deferred data-compatibility boundaries.

## Validation

Final validation on the tested implementation head:

- `go test -count=1 -timeout 900s ./...` — **PASS**.
- `go vet ./...` — **PASS**.
- `go build ./...` — **PASS**.
- `relaykit` independent `GOWORK=off go build ./...` — **PASS**.
- Frontend typecheck (`tsgo -b`) — **PASS**.
- Frontend production `rsbuild build` — **PASS**.
- Frontend lint (`oxlint`) — **0 errors**, with the existing four
  `prefer-add-event-listener` warnings in `src/hooks/use-system-config.ts`.
- Frontend full Vitest with `--maxWorkers=1 --fileParallelism=false` —
  **171/171 test files and 1408/1408 tests PASS**.
- The default parallel frontend run remained resource/timing-sensitive on this
  Windows runner (5-second UI-test timeouts/DOM races); the previously failing
  files passed in isolation and the complete single-worker suite passed without
  increasing test timeouts.
- The Windows HTTP/2 no-GetBody negative test was stabilized to assert the
  transport-independent invariant (request fails, no retry, one intact body)
  rather than Go transport error wording; the focused test passed **20/20** runs.
- Real SQLite production migration/CRUD compatibility test — **PASS**, SQLite
  server version **3.50.4**.

Database limitation for this run:

- `TEST_FIXED_MYSQL_DSN` — unset.
- `TEST_FIXED_POSTGRES_DSN` — unset.
- `TEST_MYSQL_DSN` — unset.
- `TEST_POSTGRES_DSN` — unset.

Accordingly this closeout **does not claim fresh MySQL/PostgreSQL certification**.
The repository CI matrix still defines real MySQL 5.7 and PostgreSQL 9.6 gates;
those external jobs remain required before a strict production freeze.

## Upstream rehearsal

Immediately before closeout, `upstream-official/main` was fetched again and was
still exactly:

`bdef117505247769268b209665fb3ad7554c3da7`

It is an ancestor of the tested implementation head. `git merge-tree --write-tree
HEAD upstream-official/main` completed without conflicts and produced the same
tree as `HEAD` (`9df23e4b05db0eaea84167142fa391a74b0e184c`).

This is a clean current-upstream rehearsal, but it is **not evidence** that the
future target of `<=5` manual conflicts across dozens of new upstream commits has
been achieved, because official `main` did not advance during this refactor.
That KPI can only be measured when a meaningful new upstream range exists.

## Release posture

- No production deployment was performed.
- This branch should be reviewed as one committed range from the repaired
  baseline `5afacf7da479dcf23b450a0535fbfcd67f4a39fb` through the final closeout.
- Before production freeze: run the real MySQL/PostgreSQL compatibility matrix,
  then perform the final independent code review and any environment-specific
  smoke tests required by deployment.
