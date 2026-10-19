# Modularization Plan v2.1 — Final Closeout

The earlier sections are historical release evidence. The latest upstream
integration and its current validation status are recorded in
[2026-10-19 upstream alignment closeout](#2026-10-19-upstream-alignment-closeout).

This closeout records the final measured implementation state and validation
evidence for the modularization branch. It intentionally separates dependency
ownership success from the remaining upstream-owned fork surface.

## Measurement point

- Tested candidate implementation head: `918d58061` (`test: isolate gin mode for race safety`).
  The last production-source change before the release-preflight test hardening is
  `b58d4ea36` (`fix: satisfy strict frontend lint gate`).
- Official NewAPI comparison base/current upstream main: `bdef117505247769268b209665fb3ad7554c3da7`
- Generated manifest: `docs/architecture/modularization/current-manifest.csv`
- Manifest rows: **738**
- Unclassified/default `UPSTREAM_CORE` rows: **0**
- Concrete ChuanAPI-owned paths outside `modules/chuanapi/` and
  `web/src/extensions/chuanapi/`: **0**

Final ownership distribution at the tested implementation head:

| Ownership | Files | Production files |
| --- | ---: | ---: |
| ChuanAPI module | 127 | 107 |
| Generic module | 58 | 27 |
| Generic extension kernel | 33 | 17 |
| Composition root | 6 | 3 |
| Generic core enhancement | 94 | 85 |
| Product-core customization | 252 | 251 |
| Data compatibility | 13 | 11 |
| Test/documentation compatibility | 138 | 1 |
| Ops/deployment | 17 | 7 |

The remaining upstream-owned production core surface is **347 files**:

```text
generic core enhancement       85
product-core customization    251
data compatibility             11
---------------------------------
core production surface       347
```

Therefore the earlier aspirational `<120–150` core-file target is **not met**.
The refactor achieved concrete module ownership and dependency isolation, but it
 did not turn the customized product into a thin fork. The 347-file surface must
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

Release-candidate validation through `918d58061`:

- Codex release-review overlay (`^TestFinalReview`) for Pancake retry and Wallet
  Reserve replay — **PASS** for both payment and wallet packages.
- `go test -count=1 -timeout 900s ./...` — **PASS**.
- `go vet ./...` — **PASS**.
- `go build ./...` — **PASS**.
- Root `go test -race -count=1 ./...` — **PASS** after isolating Gin's global
  test mode in package `TestMain` hooks; the discovered races were test-global
  `gin.SetMode` writes rather than production request-state races.
- `relaykit` independent `GOWORK=off` test/vet/build — **PASS**.
- `relaykit` independent `CGO_ENABLED=1 go test -race -count=1 ./...` — **PASS**.
- Root and relaykit `go mod verify` — **PASS**.
- Payment binding SQLite tests cover fresh migration, legacy upgrade, repeated
  migration, `trade_no` uniqueness, and top-up/subscription transaction rollback
  when binding persistence fails — **PASS**.
- Frontend typecheck — **PASS**.
- `bun audit --production` — **PASS / no vulnerabilities found** after pinning the
  transitive `colord` dependency to 2.9.4.
- strict `lint:ci` (`oxlint --deny-warnings`) — **0 warnings / 0 errors**.
- protected-header `format:check` — **PASS**.
- `copyright:check` — **PASS**.
- production Rsbuild — **PASS**.
- single-worker full Vitest — **171/171 test files and 1408/1408 tests PASS**.
- Initial JS referenced directly by `index.html`: **2,378,821 bytes raw / 687,897
  bytes gzip**, preserving the restored lazy-loading budget.
- The default parallel frontend run remained resource/timing-sensitive on this
  Windows runner (5-second UI-test timeouts/DOM races); the previously failing
  files passed in isolation and the complete single-worker suite passed without
  increasing test timeouts.
- The Windows HTTP/2 no-GetBody negative test was stabilized to assert the
  transport-independent invariant (request fails, no retry, one intact body)
  rather than Go transport error wording; the focused test passed **20/20** runs.
- Real SQLite production migration/CRUD compatibility — **PASS**, SQLite **3.50.4**.
- Real disposable MySQL **5.7.44** matrix — **PASS** for the production migration
  path, BIGINT schema, token/prefill/session migration checks, PaymentOrderBinding
  fresh/legacy/repeat migration, uniqueness, transaction rollback and concurrent
  provider-order binding.
- Real disposable PostgreSQL **9.6.24** matrix — **PASS** for the corresponding
  production migration, BIGINT, migration and PaymentOrderBinding scenarios.
- Release-upgrade rehearsal from rebuildable deployed-line component
  `a515b6689fb288a4d59426874dc9b98a97dc9ab1` to the current candidate and then
  old-version readback — **PASS on SQLite, MySQL 5.7.44 and PostgreSQL 9.6.24**.
  The public production version contains later build-component ids that are not
  present in any configured remote, so this is not claimed as an exact checkout
  reconstruction of the currently deployed binary.
- Release-gate unit suite — **8/8 PASS**. The owner explicitly waived the 72-hour
  canary for this release review; no synthetic 800-row canary evidence was created.
- All seven GitHub Actions workflow files pass `actionlint`.
- Linux Go cross-builds for `linux/amd64` and `linux/arm64` produced valid ELF
  binaries. Dockerfile base images are digest-pinned and Compose static checks
  confirm fail-closed image/secrets, loopback app exposure, health checks,
  healthy dependencies and the 150-second drain period.

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
- Local code, frontend, race and three-database compatibility gates are **PASS**.
- The owner explicitly waived the 72-hour/800-observation canary requirement for
  this release review. The repository gate itself was not weakened or bypassed.
- Production remains **HOLD** only for release-artifact/environment work that this
  local checkout cannot truthfully complete: a full Buildx production image for
  the exact candidate, immutable image digest, SBOM/provenance, cosign
  sign/verify evidence, and the actual production backup/restore + Compose/reverse
  proxy rehearsal. The current candidate is not pushed and this Windows runner
  has no Linux container engine, so those artifacts were not fabricated.
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
these two P2 code defects: immutable image/SBOM/signature and actual production
backup/deployment evidence are still pending, the 347-file upstream-owned
production core surface remains above the thin-fork target, and future-conflict
performance cannot be measured until official main advances by a meaningful
commit range.

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
8. `96d13ecec` — the production dependency audit is restored to zero known
   advisories by pinning `colord` to 2.9.4 within the dependency's compatible
   semver range.
9. `b58d4ea36` — the strict frontend lint gate is restored to zero warnings by
   replacing image `onload/onerror` property assignment with explicit event
   listener registration/removal.
10. `83591f830` — PaymentOrderBinding real-database compatibility becomes a
    permanent SQLite/MySQL/PostgreSQL CI gate, including legacy migration,
    uniqueness, rollback and concurrent provider-order identity binding.
11. `918d58061` — Gin test mode is initialized once per affected package so the
    full root race suite no longer races on Gin's process-global mode state.

## 2026-10-19 upstream alignment closeout

### Source identity and delivery target

- Customized source: `bfe2595741cec65b6f6e8cadfd33ded9074f0794`, from
  `codex/linuxdo-bonus-3-20261017`.
- Shared upstream baseline: `633b4c5b190dc1af99a2814391354d86948e7943`.
- Pinned official upstream target: `058c8231b68c51ee02abee15befcd720fee9f4a2`
  (`fix(audit): add composite index for audit log ordering (#7874)`).
- The integration covers **33 upstream commits / 239 upstream-changed paths**.
  The required two-parent merge preserves `bfe25957` and `058c8231`; `633b4c5b`
  identifies their shared comparison baseline, not a third merge parent.
- Candidate branch: `codex/upstream-align-20261018`.
- Source version: `v1.0.0-rc.44.chuanapi.20261018`, as recorded in `VERSION`.
- Final manifest against `058c8231`: **1154 paths, zero unclassified paths**;
  700 production, 394 test, 21 documentation, 31 operations and 8 tooling paths.

The historical ownership counts above refer to their original measurement point.
Current classification keeps the actual upstream-owned conflict surface visible;
assigning ownership labels does not establish that those files became modules.
Paths under `verification/` below refer to the accompanying verification archive,
which is delivered separately from the repository source.

### Reconciliation and preserved business rules

The authenticated layout owns one command menu and one shared search provider;
the duplicate search context is removed. Unique DeepSeek model-name, reasoning,
mapping and blacklist assertions were consolidated into the upstream test table.
Container monitoring uses the managed lifecycle worker instead of a second
unmanaged startup loop. Seven locale files retain custom and new upstream text.

**Fourteen overlapping frontend paths (10 production, 4 test) retain necessary
whitespace-only differences** required by copyright normalization or oxfmt.
The pricing filter separately retains spread syntax to satisfy the existing
`prefer-spread` lint rule. These differences are separate from functional
customizations and do not mean that only 14 paths differ from upstream. Gates
and baselines are intact.

Preserved business behavior includes split paid/bonus/reward wallets, configurable
administrator prices, Waffo/Waffo-Pancake, referrals, Cline, group aliases, public
pricing, CC Switch, async images, protected branding/assets and graceful drain.
LinuxDO verified first registration still grants USD 3 once; historical USD 5
credits retain their original value. Scoped token ownership and expiry, token
writes isolated by namespace, request privacy and atomic Redis admission leases remain.

Upstream group-specific rate keys, notification windows, container monitoring,
channel priorities, Vertex project IDs, AWS Mantle, DeepSeek tools and token
quota-query controls were reconciled with those rules. Quota-query create/edit/
legacy defaults and cached true-to-false invalidation are preserved. Public
catalogue/llms projections are allowlisted; image-model discovery checks token
ownership, scope and routeability on the server.

Task/Midjourney settlement records wallet receipts and commits funding, token
counters and task state atomically. Retry/replay preserves the original account
shares. Legacy Midjourney NULL receipts keep the paid-account refund fallback;
unavailable historic account shares are not reconstructed. Aggregate-cache
invalidation no longer applies an extra increment after concurrent hydration.

Converted Responses failures preserve observed usage, settle once and prevent
retry; a role-only converted failure refunds pre-consumption. Native Responses'
created-only prompt-charge policy remains an explicit compatibility requirement.
Both paths share cumulative hosted-tool observation and configured pricing.
SSE short writes, Flush and Ping failures stop delivery while retaining usage.
The provider-error-before-Ping regression first failed in six cases, then passed
without extra cached/error writes or replacement of the first diagnostic.

### Frontend, authentication and dependency closeout

The former production audit blocker is closed by removing `node-forge` and its
types. [GHSA-86w9-cpqp-85rv / CVE-2026-85393](https://github.com/advisories/GHSA-86w9-cpqp-85rv)
affected `node-forge@1.4.0` and listed no patched version when checked. Its sole
production caller used encryption rather than the affected signature-verification
API; that reachability distinction did not waive the failing dependency gate.

The maintained replacement components and locked versions are:

| Component | Version | Purpose | License |
| --- | --- | --- | --- |
| [micro-rsa-dsa-dh](https://github.com/paulmillr/micro-rsa-dsa-dh) | 0.4.0 | RSA-OAEP public encryption | MIT |
| [@noble/hashes](https://github.com/paulmillr/noble-hashes) | 2.4.0 | SHA-256 / MGF1 | MIT |
| [@noble/ciphers](https://github.com/paulmillr/noble-ciphers) | 2.4.0 | AES-256-GCM | MIT |
| [asn1js](https://github.com/PeculiarVentures/ASN1.js) | 3.1.0 | Bounded RSA SPKI parsing | BSD-3-Clause |

HTTPS WebCrypto stays primary. The lazy non-WebCrypto fallback retains short v1
and long UTF-8 v2 passwords, the `password-v2` OAEP label, `password-v2:<kid>` AAD,
12-byte nonce and 16-byte GCM tag. It requires native `crypto.getRandomValues`
and fails closed when unavailable. RSA OID/NULL parameters, exact SPKI structure,
trailing data, BIT STRING, 2048–16384-bit modulus and exponent bounds are checked.
No plaintext fallback, `Math.random`, audit ignore or weaker cipher was added.

Two new regressions failed against the old implementation before the repair.
The final 16 authentication tests include independent Node/OpenSSL decryption,
190/191-byte boundaries, malformed SPKI, tampering and cache recovery. Actual
production Go `common.DecryptPassword` passed **27/27 interoperability cases**
across WebCrypto, unavailable SubtleCrypto and unsupported SubtleCrypto paths.

| Final frontend gate | Observed result |
| --- | --- |
| Complete Vitest, `--maxWorkers=2` | **292 files / 2925 tests PASS**, zero failures/skips |
| Typecheck and lint | PASS; 17 existing lint warnings, zero errors |
| `lint:ci` | PASS; baseline unchanged |
| `format:check` and `copyright:check` | PASS |
| Production `bun run build` | PASS |
| `bun audit --production` | **PASS; 758 packages, zero vulnerabilities** |
| `bun install --frozen-lockfile` | PASS; no dependency/lock changes |

`verification/frontend/gates.json`, `vitest.json`, `go-password-interop.log` and
`dependency-remediation.json` contain commands, results and source hashes.
`node-forge` is absent from production source, manifests, lockfile and built JS.
[THIRD-PARTY-LICENSES.md](../../../THIRD-PARTY-LICENSES.md) records the replacement,
corrects 10 stale Axios/Go/Redis/GORM version or module entries, and adds exact
upstream notices for all four components plus new `pvtsutils`/`pvutils` utilities.
Installed/published LICENSE files were checked; existing license text is intact.

Authentication review used [OWASP ASVS 5.0.0](https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x20-V11-Cryptography.md),
the [Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html),
[Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
and [Cryptographic Storage](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html)
cheat sheets. Changed ownership, scope/expiry, email and secret-free audit controls
have focused regressions. This is scoped validation, not full ASVS certification:
retained 2048-bit RSA does not establish ASVS's full 128-bit-strength criterion;
JS BigInt is not guaranteed constant-time, and browser RSA use is public-key only.
The [noble-ciphers audit](https://github.com/paulmillr/noble-ciphers#security) covered v1.0.0 in 2024, not this entire stack.
HTTP compatibility does not replace TLS protection for page delivery or sessions.

### Database closeout with exact custom-source upgrade evidence

`verification/db-closeout/db-closeout-report.md`, `results.json`, `metadata.json`
and complete raw JSONL logs record **17 successful command groups**: 13 matrix
groups plus four final precision-verification runs. Every group has its required
test PASS and package PASS; there are zero failed, skipped or missing cases.

The earlier `final-058/database-results.json` overstated the MySQL upgrade:
`mysql-candidate-upgrade.jsonl` stopped during the second `InitDB` without a final
PASS. This closeout supersedes that incomplete evidence with newly created
isolated databases and complete logs. Earlier independent-store cases seeded a
copied rc.33 schema; the new upgrade also seeds real audit/usage records through
the exact `bfe25957` production source before candidate migration.

The baseline is `bfe2595741cec65b6f6e8cadfd33ded9074f0794`, not a live database copy.
All 1294 Go/module and existing test blobs, apart from the injected upgrade test,
match that baseline; the full tracked tree was independently checked. Only the
updated `model/release_upgrade_integration_test.go` fixture was overlaid. Candidate
production code was not injected into the seed checkout. This database closeout
changed that fixture, not production database behavior or GORM/dialect versions.

| Actual isolated engine | Fresh/repeated startup | Exact bfe seed → candidate upgrade |
| --- | --- | --- |
| SQLite 3.50.4 | PASS | PASS; shared main/log handle |
| MySQL 8.0.46 | PASS | PASS; separate main/log databases |
| PostgreSQL 16.15 | PASS | PASS; separate main/log databases |
| ClickHouse 25.8.24.21 | PASS for log store | PASS; SQLite main plus independent ClickHouse logs |

The 17 groups comprise three primary-engine fresh runs, eight seed/verify runs
across four configurations, one audit-order matrix, one split-store matrix, and
four final upgrade precision checks. Fresh migration repeats twice; upgrade seed
and verification each perform three production startup cycles. Coverage includes
paid/bonus balances, 64-bit usage, user/token uniqueness, token legacy fields and
disabled new quota-query permission, Midjourney metadata/NULL receipts/indexes,
administrator model/pricing options, and existing usage/audit content.

Final assertions preserve audit role/timestamp, false/zero JSON values and exact
uint64 `9007199254740993`, deterministic same-time ordering and relational event-ID
uniqueness. SQLite/MySQL/PostgreSQL retain old index columns/uniqueness and create
`(created_at, event_id)`; PostgreSQL checks physical key order. ClickHouse verifies
MergeTree ORDER BY and no audit TTL; it does not claim relational uniqueness.
Independent-store tests cover routing, actor visibility, repeated startup, cleanup
isolation and usage TTL versus audit retention. SQLite's current production
selector shares main/log storage; no independent SQLite DSN behavior is claimed.

Reproduction uses `verify_db_closeout.py` and `--verify-existing` in that evidence
directory with real loopback-only test engines. Results record each cwd, phase,
command, temporary DSN and exit code. Minimum-version servers were not rerun here;
historical MySQL 5.7/PostgreSQL 9.6 results remain historical evidence only.

### Earlier independent evidence and final integration gates

Go validation uses Go 1.27.2, `GOWORK=off` and `GOTOOLCHAIN=local`; frontend uses
Bun 1.4.0. The following successful runs occurred earlier in this integration.
They remain independent evidence and do not substitute for the final root rerun
or native Responses accounting checks. Go counts include nested subtests and
separate runs must not be added into one total.

| Earlier gate | Retained observed result |
| --- | --- |
| Root complete tests / race | PASS; 6263 / 6264 test/subtest events, 65 packages each |
| Four affected stream packages, complete race run | PASS; 680 test/subtest events |
| Root vet/build and `CGO_ENABLED=0` api-only build | PASS |
| Independent relaykit test/vet/build/race | PASS; race has 574 test/subtest events |
| Independent tokenkit test/vet/build | PASS; verified complete test run has 12 test/subtest events |
| Release-gate / CI Python unit suites | PASS; 8 / 14 tests |
| Real pre-consume policy matrix | PASS; 34 test/subtest events on three primary engines |
| Converted Responses HTTP-to-ledger matrix | PASS; 54 real-DB scenarios across three engines |
| Four-provider converted protocol/tool contracts | PASS; 24 cases |
| Task/Midjourney transaction/replay matrix | PASS; 45 scenarios across three engines |
| `TestModelRateLimitRealRedis`, race enabled | PASS against isolated Redis 7.0.15 |

Final validation on the frozen source completed **all 14 gate commands** at
2026-10-19 00:45 UTC with exit 0 for each command:

| Final gate | Observed result |
| --- | --- |
| Root complete normal / race | **6400 / 6400 PASS test/subtest events**, 65 test packages each; zero failures or unclosed packages |
| Root vet/build | PASS |
| Independent relaykit vet/build/normal/race | PASS; normal and race each have 574 PASS events, 9 test packages, zero skipped tests |
| Independent tokenkit vet/build/test | PASS; 12 PASS events, one test package, zero skipped tests |
| `CGO_ENABLED=0` api-only build | PASS |
| Release-gate / CI-evidence Python suites | PASS; 8 / 14 tests |

Each root run separately records 96 environment-dependent/helper skipped test
items and 38 packages without tests; these are not counted as PASS. External
database and Redis checks use the separate real-service evidence above. Live
provider tests were not enabled. Counts from separate runs are not summed.

`verification/final-go/results.json` and the complete raw logs record those
results. The harness's final inventory check initially returned exit 1 because
the two Python gates generated five untracked `__pycache__/*.pyc` files. Removing
only those generated outputs proved all **3141 original source/test inputs**
unchanged. `source-reconciliation.json` retains this explanation and hashes;
the original metadata/logs are intact. Both Python suites passed again with
`PYTHONDONTWRITEBYTECODE=1` and an unchanged inventory. `acceptance.json` records
the reconciled final acceptance, rather than hiding the raw inventory result.

Final native Responses validation: **285 leaf accounting scenarios PASS**
(95 per SQLite/MySQL/PostgreSQL; 289 PASS nodes including parent suites), with
zero failures/skips and complete package PASS. The final matrix ran at
2026-10-19 00:30:32–00:30:37 UTC; all 10 production/test source hashes stayed
unchanged. `verification/native-responses/database-metadata.json` and
`database-matrix.jsonl` record the exact command, temporary main/log databases,
versions, source hashes and results. The separate final compatibility run has
137 leaf PASS / 144 PASS nodes across relay and controller; its four optional
external-engine branches were covered by the non-skipped real three-engine run.

Native HTTP nonstream, HTTP SSE and WebSocket now release pre-consumption when
the upstream explicitly fails or cancels without reported usage, generated
content or a completed tool. Successful zero-token fixed pricing, successful
missing-usage estimates and the existing created-only disconnect prompt estimate
are preserved. Partial text/reasoning/refusal/audio, code and tool arguments,
completed zero-argument tools, actual tool actions and native usage retain the
existing settlement path. Echoed request input and empty in-progress tool shells
do not become chargeable output. Zero-settlement transaction failure uses the
existing refund recovery; Gemini blocked/empty failures keep their prior policy.

Nested failed/cancelled terminal status is classified as failure. WebSocket
`response.error` ends the current request immediately, allowing a second create
on the same connection. Actual controller socket tests separately verify frame
delivery and sanitization, error correlation, close codes, no extra upstream
connection, and health accounting. They are not described as full socket flows
on every database engine. Earlier assertions that charged empty explicit failures
were corrected consistently with the successful-request fixed-pricing contract in
`pkg/billingexpr/expr.md`; their connection and health assertions remain intact.
Independent review covered the production change and the final fixture/assertion
updates before source freeze.

### Delivery boundary

The deliverable remains the isolated candidate branch and reviewable PR. It does
not merge into `main`, change the source customization branch, touch production
data or deploy production. Image digest/SBOM/signing, production backup/restore
and canary evidence are not manufactured by this closeout. Historical release
waivers and historical zero-advisory results do not apply to this candidate.
