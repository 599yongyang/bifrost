# Moon v1.6.10 → v2.0.0 commit disposition

This ledger accounts for every Moon host commit after `transports/v1.6.10`. The original upgrade
plan recorded 34 commits before the v1 auth hotfix was added; the protected v1 branch now contains
35 commits. The v2 branch intentionally reimplements behavior on the official v2 architecture
instead of merging the v1 branch.

| v1 commit | Behavior | Disposition | v2 evidence |
| --- | --- | --- | --- |
| `f288c5204` | Preserve image-edit requests across fallback | `adapt-to-v2` | `e88e8c975`; `core/fallback_request_test.go` |
| `0c5cf64b8` | Dynamic glibc image | `adapt-to-v2` | `600847b25`; `transports/Dockerfile.dynamic-debian` |
| `79d2fa4e2` | Build image with forked core | `adapt-to-v2` | `600847b25`; Docker workspace assertions |
| `f33ab311f` | Provider timeout-source diagnostics | `adapt-to-v2` | `70356fcce`; core/provider timeout tests and restored log-detail UI |
| `5532b619e` | Image request observability | `reimplement` | `07da5a1c2`, `6abd87c04`; tracing/media tests |
| `6db37a1db` | Safe selective image exports | `reimplement` | `1bf1a3b3b`, `6abd87c04`; OTEL selection/media tests |
| `39c8c142a` | Selective-export rule UI | `adapt-to-v2` | `9f5c0bd8a`; OTEL schema and observability export UI tests |
| `d436a76f4` | English/Chinese UI | `reimplement` | `a4fbd3e89`, `ac15335ba`; language/copy tests |
| `9eedaa606` | Localization coverage | `reimplement` | `a4fbd3e89`, `ac15335ba`; navigation/dashboard/log copy tests |
| `380f5fce3` | Simplified selective-export policies | `adapt-to-v2` | `1bf1a3b3b`, `6760008ae`; candidate sampling, technical-quality, media-policy and UI tests |
| `6332a7991` | LLM-log latency filters | `adapt-to-v2` | `4ed6d53a2`; log handler/model tests |
| `885423f3a` | Manual Langfuse export | `reimplement` | `585665f5e`, `d2b59e37e`, `47f4a62b4`, `eebbbcde8`, `9f5c0bd8a` |
| `68127a2c4` | Reduce dynamic-build disk use | `adapt-to-v2` | `0b77aa8c4`, `b000b4b2f`; bounded reusable caches |
| `a268f3169` | Rebrand response headers | `adapt-to-v2` | `3d66e5d27` plus the v2 tracing-middleware regression test; provider/routing headers remain private and the public trace header is `x-moon-trace-id` |
| `35f0ec65c` | Hide internal routing headers | `adapt-to-v2` | `3d66e5d27`; handler/integration privacy tests |
| `c5f066071` | Reject image responses without image data | `adapt-to-v2` | `e88e8c975`; image response validation tests |
| `5937c6d3e` | Reject empty completed image streams | `adapt-to-v2` | `e88e8c975`; stream fallback/truncation tests |
| `c24411788` | Remove community/bug-report links | `adapt-to-v2` | `6d6179c57`; v2 topbar/login policy |
| `5dd4f5e36` | Upstream correlation IDs in logs | `adapt-to-v2` | `fbbdfe38d`; logging and UI detail coverage |
| `3519b23fd` | Ignore local build artifacts | `adapt-to-v2` | `b000b4b2f`; `.dockerignore` and Git ignore policy |
| `31f977086` | OSS alerting and provider reliability | `reimplement` | `1cbf68c83`, `e969cc2ef`, `6fad88946`; store/engine/UI tests |
| `426016f4a` | Persist selective-export settings | `adapt-to-v2` | `567a01c1c`, `6760008ae`; canonical serialization and legacy normalization tests |
| `c0e37222f` | Harden alert evaluation/history UX | `reimplement` | `1cbf68c83`, `e969cc2ef`, `6fad88946`, `567a01c1c`, `72c667376`; alerting tests |
| `5b400d2e3` | Moon maintenance/deployment guide | `reimplement` | `33053af6f`; `docs/MAINTENANCE.md` |
| `ea74d890f` | Hide gateway identity from client errors | `adapt-to-v2` | `3d66e5d27`; error sanitizer tests |
| `13bef5d0d` | Expand i18n coverage | `reimplement` | `a4fbd3e89`, `ac15335ba`; language/copy tests and timeout diagnostics |
| `2e49d0ed0` | Header-driven circuit breaker and UI | `reimplement` | `605364b32`, `9f9011a05`, `4ff400050`; circuit tests |
| `5d1704e2d` | Error-aware fallback routing | `reimplement` | `96fcdce15`, `425ce08ef`, `f5b862d64`, `493672a89`, `28215607d`, `28c78eb46`; direct HTTP and multipart coverage |
| `423b4b401` | Plugin panic containment | `adapt-to-v2` | `b288c55d6`, `19336a043`, `28af37074`, `294804389`, `28c78eb46`; plugin, health-probe and cleaner panic tests |
| `c0da8aa63` | Routing-rule request statistics | `adapt-to-v2` | `e722cb079`, `4ed6d53a2`; ranking/redaction tests |
| `e56260d0c` | Preserve error-fallback policies at runtime | `reimplement` | `28215607d`; unary/stream fallback tests |
| `938bcf552` | Scheduled daily quality reports | `reimplement` | `ecf7c477d`, `d15cd64b9`, `ed17a9ae7`, `567a01c1c`; report tests and legacy URL compatibility |
| `57cb95e1a` | Local governance module in dynamic image | `adapt-to-v2` | `600847b25`; Docker workspace module assertions |
| `db9885de4` | Daily reports as background jobs | `reimplement` | `ecf7c477d`, `d15cd64b9`, `ed17a9ae7`, `567a01c1c`; job/idempotency and browser-refresh recovery |
| `0d3a7d74d` | Restrict `/api/dev/` auth whitelist | `upstream-superseded` | v2 already uses the trailing-slash prefix; middleware regression tests |

The sibling Moon plugin branch is based directly on the complete protected plugin `main` history.
Its v2-only commits migrate the ABI/toolchain, add `gpt-image-2` auto-size accounting, preserve clean
VCS metadata, and enforce the shared bounded cache. No plugin `main` commit is absent from
`moon/bifrost-v2.0.0`.

## Production migration closure

The final production-readiness pass closed gaps that were not visible in the original commit-level
inventory:

- `28c78eb46` restores request-level `error_fallbacks` across JSON and multipart inference paths,
  timeout trace metadata, OTEL operational event metrics, and panic isolation for health probes and
  retention cleanup.
- `567a01c1c` restores canonical OTEL selective-export persistence, ordered policy-card editing,
  daily-report job recovery after browser refresh, current-form generation, the legacy reports URL,
  and safe-but-useful alert failure details.
- `6760008ae` restores the latest OTEL media candidate rate, atomic complete-record contract,
  technical-quality policies, head capture decisions, pinned policy snapshots, and fail-closed panic
  isolation before request-sized image payloads are copied.
- `72c667376` restores the separate Alert Rules, Alert Channels, and Alert History RBAC resources and
  adds a real browser workflow assertion for the latest OTEL policy editor.

These entries supersede earlier broad claims that the corresponding v1 behavior was already fully
covered by the first v2 migration commits.
