# Moon Bifrost 2.0.0-moon.3 release manifest

Generated: 2026-08-29 (Asia/Shanghai)

Status: local release candidate. This revision prevents an incompatible Go shared-object plugin
from terminating the gateway during startup; the failed plugin is marked `error` while the base
gateway remains available for administrative recovery.

## Source and artifacts

| Item | Value |
| --- | --- |
| Host binary source commit | `80a4f948c9c6d757bfdfb10602f97e10ec186ba2` |
| Plugin source commit | `c42113512dd4188a3c970285913489c4ff828b1e` |
| Image tag | `bifrost-moon:2.0.0-moon.3` |
| Local image ID | `sha256:c65a868174e57804e7a01172d3801c792fe942ac7453557c1e5daf305aabd5ed` |
| Image tar | `bifrost-moon-2.0.0-moon.3-amd64.tar` |
| Image tar SHA-256 | `4cdd60004b614bf7ccde4d81d68286e18c32e3324ff5308d00e9f7432f68752a` |
| Plugin | `bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.3.so` |
| Plugin SHA-256 | `a5c46faf8f9950fd8273182f375cd44f37b4355c60303eb257dcab9f5b363ee0` |
| Toolchain / target | Go 1.27.0; linux/amd64; CGO; Debian/glibc |
| Runtime user | `1000:0` |
| Registry RepoDigest | Pending image push |

The plugin reports `vcs.modified=false`, the exact plugin source revision above,
`-buildmode=plugin`, and `github.com/maximhq/bifrost/core (devel)` from the paired fork workspace.

## Incompatible-plugin regression

The candidate was started with an enabled custom-plugin path containing a Go 1.26.5 Bifrost binary,
which reproduces the production failure class without invoking `plugin.Open` on incompatible module
data. Verification showed:

- `/health` returned healthy database pings.
- `/api/plugins` returned the incompatible plugin with `status=error`.
- The status log reported `plugin was built with Go go1.26.5 but host uses go1.27.0`.
- Bifrost completed startup and kept all built-in plugins active.
- No `runtime: plugin has empty pluginpath` fatal error occurred.

Unit, targeted race, full package, and vet checks passed for the shared-object loader and HTTP server.

## Operational meaning

Fail-open applies to the individual custom plugin, not to its features. If the Moon plugin is in
`error`, the base Bifrost gateway is available for UI/API recovery, but Moon routing, request
sanitization, identity-hiding, and image compatibility must be treated as unavailable until the
matching plugin path is activated.

Production configuration audit, production-sized migration, live-provider matrix, registry push,
staged canary, and rollback proof remain separate release gates.
