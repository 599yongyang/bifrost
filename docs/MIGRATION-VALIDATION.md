# Moon Bifrost v2 Migration Validation

Validation date: 2026-08-29 (Asia/Shanghai)

This document records local migration-candidate evidence. It is not a production release approval.
The repository `config.json` is an empty placeholder, and no production database copy or live
provider credentials were available during this run.

Sanitized command/output evidence is retained in
[validation/moon-v2-candidate-2026-08-29.md](validation/moon-v2-candidate-2026-08-29.md).

## Candidate inputs

| Item | Value |
| --- | --- |
| Host branch | `moon/bifrost-v2.0.0` |
| Host binary source HEAD | `ac15335ba` (the later `07132223f` changes only the audit command/docs) |
| Host repository validation commit | `07132223f` |
| Plugin branch | `moon/bifrost-v2.0.0` |
| Plugin validation commit | `0df9dace40b22f7ff87a4d90549ab7f0674df056` |
| Candidate image | `bifrost-moon:2.0.0-migration-candidate` |
| Image ID | `sha256:334be646727827c76ccf9540142f2e2204ed2976d4d03cd6738608851c3d5c64` |
| Image size / platform | 391,363,474 bytes; `linux/amd64` |
| Plugin SHA-256 | `495a601adbdd6bf70c62466ed0cdc562e3638789b1153a7349227c91fd9335ad` |

The candidate tag is deliberately not a production `moon.N` release. Rebuild the final image and
plugin from the selected final commits and record new immutable digests before deployment.

## Build and ABI evidence

- Dynamic host image built successfully with `transports/Dockerfile.dynamic-debian`.
- The builder verified every shared module resolved to the current fork workspace.
- Host binary is Go 1.27.0, `linux/amd64`, `CGO_ENABLED=1`; `ldd` resolved `libc.so.6`.
- Moon plugin is Go 1.27.0, `linux/amd64`, `CGO_ENABLED=1`, `-buildmode=plugin`.
- Plugin VCS metadata is clean: `vcs.modified=false`, revision `0df9dace...`.
- A candidate container loaded the plugin from `/app/data/plugins/...so`; the plugin API reported
  `active` with `llm`, `mcp`, and `http` types.

## Functional evidence

- `/health` returned `status=ok` with database pings healthy.
- A real `/v1/chat/completions` request traversed the candidate gateway and a local
  OpenAI-compatible upstream, returning the expected model, content, and 5/3/8 token usage.
- Moon HTTP pre-hook execution was proven without an upstream call: unsupported
  `gpt-image-2` size `123x456` returned HTTP 400 with `invalid_request_error`.
- The compatibility audit command ran independently with `GOWORK=off`; race tests and `go vet`
  passed. Repository caller scans found no active `x-bf-prom-*` or legacy routing API callsites in
  UI/scripts/examples.

## SQLite migration and rollback rehearsal

1. Loaded the existing `bifrost-moon:1.6.10-moon.31` image from the retained tar archive.
2. Started it with an isolated SQLite data directory and completed its migrations.
3. Added a sentinel log row, then stopped v1.6 and copied the data directory.
4. Started the v2 candidate against only the copy. v2-specific log/MCP/cost/overhead migrations
   completed and the clean Moon plugin loaded successfully.
5. The sentinel retained provider, model, status, routing rule, and scalar `total_tokens=42`.
6. Restarting v2 reported no pending config migrations and current log migrations.
7. Restarting v1.6 against its untouched original directory remained healthy and retained the
   sentinel, demonstrating the independent rollback path.

This rehearsal used generated SQLite data, not a production database copy. It proves migration
mechanics and idempotence, but not production duration, disk headroom, index growth, or every legacy
row shape.

## Local performance smoke test

The following numbers were measured on an ARM host running the `linux/amd64` image through Docker
emulation and a local mock upstream. They are regression smoke signals, not production capacity
claims.

| Path | Requests / concurrency | Failed | RPS | p50 | p95 | p99 | Max |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `/health` | 1,000 / 20 | 0 | 7,710.28 | 2 ms | 4 ms | 14 ms | 29 ms |
| `/v1/chat/completions` | 1,000 / 20 | 0 | 3,409.55 | 5 ms | 10 ms | 27 ms | 34 ms |

The chat run persisted 1,001 successful rows with token usage 5/3/8. Three earlier diagnostic
requests were correctly logged as errors before the mock key allowlist/private-network settings
were fixed.

## Production gates still required

- Run `moon-v2-audit` against a secure copy of the actual production config and deployment/caller
  manifests; resolve every `ERROR` and sign off every `WARN`.
- Record the production database engine/version, database and index sizes, logs row count, write
  rate, available disk headroom, backup/restore duration, and migration duration on a full copy.
- Run live-provider Chat, Responses, streaming, Images generation/edit/variation, Files/Batch and
  fallback/Circuit Breaker scenarios using canary credentials and budgets.
- Validate alert/report delivery with non-production channels and confirm secrets never appear in
  UI/API/history/log payloads.
- Build final host/plugin artifacts from clean final commits, generate the release manifest, and
  deploy with isolated data at 1% -> 10% -> 50% -> 100% while retaining the paired v1.6 rollback
  image, plugin, config, and pre-migration data snapshot.
