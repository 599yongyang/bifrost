# Moon Bifrost 2.0.0-moon.2 release manifest

Generated: 2026-08-29 (Asia/Shanghai)

Status: local release candidate; production configuration, production-sized migration, live-provider
matrix, staged canary, and registry push remain release gates.

## Source and artifacts

| Item | Value |
| --- | --- |
| Host binary source commit | `ce228be062db1f864fffd0111fa2ac4488f6f2eb` |
| Plugin source commit | `868ecfcd29fa1947aee08d4c0559061b176fd38b` |
| Image tag | `bifrost-moon:2.0.0-moon.2` |
| Local image ID | `sha256:e6228e494b9ece6a2144bfb9c4d5f897c0db7d50d077aa2129ae83bd3891de8a` |
| Image tar | `bifrost-moon-2.0.0-moon.2-amd64.tar` |
| Image tar SHA-256 | `8f8343830a2b2dfa81978c300a8309be4759190f7481d40dfb7f5c83368a1648` |
| Plugin | `bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.2.so` |
| Plugin SHA-256 | `242c644adaff63393e2e312e7356f3f1dec602a588e090daaac462a28038b583` |
| Toolchain / target | Go 1.27.0; linux/amd64; CGO; Debian/glibc |
| Runtime user | `1000:0` |
| Registry RepoDigest | Pending image push |

The plugin reports `vcs.modified=false`, the exact plugin source revision above, and
`github.com/maximhq/bifrost/core (devel)` from the paired fork workspace.

## Local verification

- UI: 29 Vitest files and 346 tests passed; TypeScript typecheck and enterprise build passed
  with 9,096 transformed modules.
- Host: affected Go package tests and vet passed during the migration audit; focused timeout tests
  passed through the unified v2 cache after the cache fix.
- Plugin: full linux/amd64 plugin test suite and paired `.so` build passed.
- Image: Docker build asserted all locally modified modules resolve from the fork and `ldd` found
  `libc.so.6`.
- Fresh isolated SQLite startup returned healthy database pings and loaded
  `bifrost-moon-response-sanitizer` as active.
- A second startup reported no pending config migrations, current log migrations, healthy database
  pings, and the Moon plugin active again.

## Production fields still required

- Registry RepoDigest after push.
- Production config checksum and `moon-v2-audit` result.
- Production database snapshot identifier/checksum, restore proof, migration duration, and disk
  growth from a full isolated copy.
- Live-provider functional matrix and v1/v2 performance A/B result.
- Canary stage evidence and rollback-drill result.

Do not mark this manifest production-approved until every field above is recorded. Deploy the image,
plugin, configuration, and migrated data as one versioned unit; retain the matching v1.6 rollback
unit separately.
