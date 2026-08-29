# Moon v2 Candidate Validation Evidence

Captured: 2026-08-29 (Asia/Shanghai)

This is a sanitized command transcript from an isolated local candidate run. Temporary containers
and data directories were deleted after capture. The local candidate images and ignored Moon plugin
build artifact were retained. No production config values, credentials, response content, or source
lines are included.

## Dynamic host image

Command:

```sh
docker build --platform linux/amd64 \
  --build-arg VERSION=2.0.0-migration-candidate \
  -f transports/Dockerfile.dynamic-debian \
  -t bifrost-moon:2.0.0-migration-candidate .
```

Relevant output:

```text
go build -ldflags="-w -s -X main.Version=v2.0.0-migration-candidate" -p=2 -trimpath
libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6
writing image sha256:334be646727827c76ccf9540142f2e2204ed2976d4d03cd6738608851c3d5c64
```

Inspection:

```text
id=sha256:334be646727827c76ccf9540142f2e2204ed2976d4d03cd6738608851c3d5c64
size=391363474
architecture=amd64
os=linux
```

## Moon plugin build and ABI

Command:

```sh
scripts/build-fork-plugin.sh 2.0.0-migration-candidate
```

Relevant output:

```text
github.com/maximhq/bifrost/core => /bifrost/core
Built: .../bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-migration-candidate.so
```

Artifact evidence:

```text
SHA-256 495a601adbdd6bf70c62466ed0cdc562e3638789b1153a7349227c91fd9335ad
go1.27.0
-buildmode=plugin
CGO_ENABLED=1
GOARCH=amd64
GOOS=linux
vcs.revision=0df9dace40b22f7ff87a4d90549ab7f0674df056
vcs.modified=false
```

## Fresh candidate startup and plugin load

Sanitized status output:

```json
{"components":{"db_pings":"ok"},"status":"ok"}
[
  {
    "name": "bifrost-moon-response-sanitizer",
    "enabled": true,
    "status": "active",
    "types": ["llm", "mcp", "http"]
  }
]
```

Startup log excerpts:

```text
loading custom plugin from path /app/data/plugins/bifrost-moon-response-sanitizer-...so
plugin status: bifrost-moon-response-sanitizer - active
successfully started bifrost, serving UI on http://0.0.0.0:8080
```

Second startup against the same fresh v2 data directory:

```text
[configstore] no pending migrations; skipping migration run
[logstore] migrations already current; skipping migration lock
plugin status: bifrost-moon-response-sanitizer - active
```

## v1.6 -> v2 SQLite migration copy

Source image loaded from the retained archive:

```text
Loaded image: bifrost-moon:1.6.10-moon.31
```

A sentinel was inserted only into the disposable v1.6 source database after stopping the writer:

```text
moon-v1-migration-sentinel|moon-test|moon1.0|success|42
```

The v1.6 directory was copied, and v2 was started only against the copy. v2 migration excerpts:

```text
mcp_tool_logs_add_redaction_mapping_column: adding column redaction_mapping
mcp_tool_logs_add_endpoint_columns: adding device_id, app_key, decision, source
mcp_tool_logs_add_plugin_logs_column: adding plugin_logs
logs_add_video_edit_input_column: adding video_edit_input
logs_add_upstream_and_overhead_latency_columns: adding upstream_latency, overhead_latency
logs_add_batch_debug_column: adding batch_debug
logs_add_cost_breakdown_columns: adding input_cost, output_cost, additional_cost
logs_add_overhead_breakdown_column: adding overhead_breakdown
plugin status: bifrost-moon-response-sanitizer - active
successfully started bifrost
```

Post-migration sentinel query:

```text
id                          provider   model    status   total_tokens  routing_rule_id  routing_rule_name
moon-v1-migration-sentinel  moon-test  moon1.0  success  42            rule-sentinel    Sentinel Rule
```

Restarting the migrated v2 copy:

```text
[configstore] no pending migrations; skipping migration run
[logstore] migrations already current; skipping migration lock
plugin status: bifrost-moon-response-sanitizer - active
successfully started bifrost
```

Restarting v1.6 against its untouched source directory:

```text
{"components":{"db_pings":"ok"},"status":"ok"}
moon-v1-migration-sentinel  moon-test  moon1.0  success  42
```

## End-to-end request evidence

Chat request through v2 and a local OpenAI-compatible mock upstream:

```json
{
  "id": "chatcmpl-moon-mock",
  "object": "chat.completion",
  "model": "openai/test-model",
  "content": "moon migration ok",
  "usage": {"completion_tokens":3,"prompt_tokens":5,"total_tokens":8}
}
```

Moon HTTP pre-hook short-circuit (`gpt-image-2`, unsupported `123x456` size):

```text
HTTP 400
{"type":"invalid_request_error","message":"unsupported gpt-image-2 size: 123x456"}
```

Post-smoke database summary:

```text
status   count  min_tokens  max_tokens
error    3      0           0
success  1001   8           8
```

The three errors were the expected diagnostics before adding the explicit v2 key model allowlist
and private-network opt-in for the local mock provider.

## Local emulated performance smoke

Environment: ARM host, Docker `linux/amd64` emulation, local mock upstream, 20 concurrent clients.

```text
/health
Complete requests: 1000
Failed requests: 0
Requests per second: 7710.28
p50 2ms; p95 4ms; p99 14ms; max 29ms

/v1/chat/completions
Complete requests: 1000
Failed requests: 0
Requests per second: 3409.55
p50 5ms; p95 10ms; p99 27ms; max 34ms
```

These figures are smoke-test evidence only and must not be used as production capacity targets.
