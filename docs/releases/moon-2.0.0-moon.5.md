# Moon Bifrost 2.0.0-moon.5 release manifest

Generated: 2026-08-29 (Asia/Shanghai)

Status: superseded by `2.0.0-moon.6`; do not deploy. The moon.5 OTEL policy UI was an approximate
rewrite rather than the final v1 component. This revision otherwise closes the remaining code-level v1→v2
parity gaps found in the production-readiness audit. Production data backup, a cloned-data migration
rehearsal, live-provider canary traffic, and rollback timing remain deployment gates.

## Source and artifacts

| Item | Value |
| --- | --- |
| Host binary source commit | `b314658ff88432e6d8cf39e0afa823ba90a2d0f0` |
| Plugin source commit | `c42113512dd4188a3c970285913489c4ff828b1e` |
| Image tag | `bifrost-moon:2.0.0-moon.5` |
| Local image ID | `sha256:729e1967fbaf29ee3b8921bf443f46425bd7cf591333ea1c65b4dda19e04c3bc` |
| Image tar | `bifrost-moon-2.0.0-moon.5-amd64.tar` |
| Image tar SHA-256 | `f7b1363dccc5944aa1e5da114adab519035510be853d086f12785566e89473ea` |
| Plugin | `bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.5.so` |
| Plugin SHA-256 | `75f153d469e29f9165580bc65d321d84c66a557e52bc51e8445002ffb08ca080` |
| Toolchain / target | Go 1.27.0; linux/amd64; CGO; Debian/glibc |
| Runtime user | `1000:0` |
| Registry RepoDigest | Pending image push |

## Closed migration gaps

- Direct JSON and multipart inference requests now preserve `error_fallbacks` without forwarding the
  Bifrost-only field to providers.
- Timeout source, configured timeout, elapsed duration, and upstream-response state reach tracing and
  Langfuse metadata.
- Health probes and retention cleanup contain dependency panics instead of crashing the process.
- OTEL operational selection/media outcomes are exported as bounded-cardinality metrics.
- OTEL selective-export configuration is persisted rather than dropped during profile saves; common,
  trace-only, and metrics-only headers serialize correctly.
- The latest ordered policy-card UI, balanced template, candidate head-sampling rate, atomic complete
  records, technical-quality matching, policy snapshot pinning, and fail-closed media capture are
  restored on the v2 multi-profile architecture.
- Alert rules, channels, and history again use separate RBAC resources. Failure details remain useful
  after URL/credential redaction.
- Daily-report jobs recover across browser refreshes, use the current form for both generation modes,
  refresh history after completion, and retain the old `/workspace/alerting/daily-reports` URL as a
  redirect.

## Verification

- Core: root and schemas tests passed with local-socket access. The environment rewrites public DNS
  test names to private addresses, so `TestValidateExternalURL` was isolated; the remainder passed
  with `-skip '^TestValidateExternalURL$'` and the URL-validator code was not changed.
- Framework: complete `tracing` and `logstore` package suites passed.
- HTTP: handlers, lib, server, and integrations package suites passed.
- OTEL: the complete plugin suite passed, including media head sampling, selection persistence,
  timeout bounds, manual export, and observability metrics.
- UI: focused Vitest suites passed (15 alerting/report tests and 5 OTEL schema/config tests), TypeScript
  typecheck passed, and the production Vite build completed with 9,102 transformed modules.
- Browser: the live development UI rendered the ordered OTEL policy cards, expanded advanced editing,
  and redirected the legacy daily-report URL to `/workspace/alerting/reports`.
- Runtime: an isolated linux/amd64 container completed fresh SQLite migrations, reported the Moon
  plugin as `active`, returned `{"components":{"db_pings":"ok"},"status":"ok"}`, reached Docker
  `healthy`, and `/app/main` dynamically linked `libc.so.6`.
- Cache policy: all Go/plugin work reused `/private/tmp/moon-bifrost-v2-cache` (8.0 GiB cap) and Docker
  reused the fixed `moon-bifrost-v2-*` BuildKit cache IDs; no per-release cache directory was created.

## Production rollout gates

1. Back up the v1 data directory/database and record a rollback timestamp.
2. Run moon.5 against a cloned production dataset and verify database migrations plus alert/report
   history counts.
3. Upload the moon.5 `.so` under its new filename; do not reuse a previously failed plugin path.
4. Start a separate canary port and data directory. Confirm the plugin is `active`, then exercise
   chat, streaming, image, request-level fallback, routing-rule fallback, OTEL, alerting, and reports.
5. Shift traffic gradually while watching 5xx, timeout source, fallback index, OTEL selection events,
   log-write drops, memory, and disk usage.
6. Keep the v1.6.10 container and untouched v1 data backup available until the observation window is
   complete. Roll back image and data together; never point v1 at a database already migrated in place.
