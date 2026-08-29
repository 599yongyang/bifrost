# Moon Bifrost 2.0.0-moon.4 release manifest

Generated: 2026-08-29 (Asia/Shanghai)

Status: local release candidate. This revision restores Moon's error-aware fallback management UI
on top of the v2 routing plugin while retaining moon.3's incompatible-plugin degraded startup.

## Source and artifacts

| Item | Value |
| --- | --- |
| Host binary source commit | `cb2852c2270b26a9ba87088297562a0d7d785d1d` |
| Plugin source commit | `c42113512dd4188a3c970285913489c4ff828b1e` |
| Image tag | `bifrost-moon:2.0.0-moon.4` |
| Local image ID | `sha256:37d25617d43d6942933ec45b6efb87f52df40e19c01076aa24c237b7a62b470b` |
| Image tar | `bifrost-moon-2.0.0-moon.4-amd64.tar` |
| Image tar SHA-256 | `cd4737ea6139a8a2cdba6bce95ae649e257874753c9788672f26ba436629368a` |
| Plugin | `bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.4.so` |
| Plugin SHA-256 | `a5c46faf8f9950fd8273182f375cd44f37b4355c60303eb257dcab9f5b363ee0` |
| Toolchain / target | Go 1.27.0; linux/amd64; CGO; Debian/glibc |
| Runtime user | `1000:0` |
| Registry RepoDigest | Pending image push |

## Restored fallback behavior

- Ordinary ordered fallback chains remain available in the Routing Rule editor and detail sheet.
- Error-aware rules can be created and edited by scenario: content policy, unsupported operation,
  rate limit, authentication, billing, permission, timeout, provider unavailable, network, invalid
  request, internal, or unknown.
- Optional provider, error type/code, status code, and message clues supplement scenario matching.
- Existing legacy `when` rules open in expert mode and preserve their exact payload when untouched.
- Dedicated fallback targets can be added, removed, and reordered; empty and duplicate chains are
  rejected before submission.
- Routing Rule details show the matcher and ordered dedicated fallback chain.
- English and Chinese copy is available for the restored management surface.

## Verification

- UI: 30 Vitest files and 350 tests passed; TypeScript typecheck, scoped lint, and enterprise build
  passed with 9,098 transformed modules.
- Backend: targeted core, routing plugin/rules, and HTTP routing fallback tests passed.
- Runtime: the moon.4 image started healthy with the matching Moon plugin active.
- Browser verification opened `/workspace/routing-rules`, created a new rule sheet, added an error
  fallback, and confirmed scenario selection, supplemental clues, and ordered target controls render.

Production configuration audit, production-sized migration, live-provider fallback matrix,
registry push, staged canary, and rollback proof remain separate release gates.
