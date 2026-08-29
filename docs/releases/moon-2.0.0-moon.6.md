# Moon Bifrost 2.0.0-moon.6 release manifest

Generated: 2026-08-29 (Asia/Shanghai)

Status: production migration candidate. Supersedes moon.5 because moon.5 contained an approximate
OTEL policy editor instead of Moon v1's final editor.

## Source and artifacts

| Item | Value |
| --- | --- |
| Host binary source commit | `e1c6fb514` |
| Plugin source commit | `c42113512dd4188a3c970285913489c4ff828b1e` |
| Image tag | `bifrost-moon:2.0.0-moon.6` |
| Local image ID | `sha256:423a35247700dc83b7b6999367ea59af4451e56a3d011bd9dfdd918c06501274` |
| Image tar | `bifrost-moon-2.0.0-moon.6-amd64.tar` |
| Image tar SHA-256 | `2212be2f5ca7f0d8cf85d2e7ee85cd3040d52efe6587c151133feea387680441` |
| Plugin | `bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.6.so` |
| Plugin SHA-256 | `75f153d469e29f9165580bc65d321d84c66a557e52bc51e8445002ffb08ca080` |
| Toolchain / target | Go 1.27.0; linux/amd64; CGO; Debian/glibc |

## OTEL UI correction

- The selective-export component is taken directly from the final `moon/bifrost-v1.6.10`
  implementation and adapted only at the translation boundary.
- A normalized source diff shows no structural or interaction differences from the v1 component;
  the only additions are four `data-testid` attributes used by browser tests.
- Restored behavior includes drag-to-reorder, fixed catch-all policy behavior, percent badges,
  four-column primary conditions, collapsible additional conditions, the two-column action area,
  balanced sampling insertion, and advanced resource controls.
- The v2 multi-profile connector, independent trace/metrics endpoints, and signal-specific headers
  remain intact outside this component.

## Verification

- OTEL schema/config/normalization Vitest suites passed (5 tests).
- TypeScript typecheck and the production Vite build passed with 9,103 transformed modules.
- Browser verification in Chinese confirmed the five default policies, drag handles, summaries,
  immutable catch-all card, expanded primary conditions, expanded additional conditions, and action
  controls against the supplied reference screenshots.
- The linux/amd64 image reached Docker `healthy`; the Moon plugin loaded as `active` without ABI or
  package-version errors.

All runtime, fallback, tracing, alerting, reporting, canary, and rollback gates documented in the
moon.5 manifest remain applicable.
