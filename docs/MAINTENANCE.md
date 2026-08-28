# Moon 分支维护与发布指南

本仓库是 Moon 生产环境使用的 Bifrost fork。当前升级分支以官方
`transports/v2.0.0` 为基线；稳定回滚分支 `moon/bifrost-v1.6.10` 保持独立，升级期间
不要将两个分支的镜像、数据库副本或 Go 插件混用。

Moon 插件的构建、发布和 canary 操作详见相邻仓库
`../bifrost-moon-plugin/docs/RELEASE.md`。

## 仓库和 ABI 要求

```text
project/
├── bifrost/
└── bifrost-moon-plugin/
```

Moon 使用 Go `-buildmode=plugin`。宿主和 `.so` 必须使用相同的 Go 工具链、
`linux/amd64`、Debian/glibc，以及同一份 `core`、`framework`、`transports` 和共享插件
源码。只要其中一项不同，即使文件能够复制进容器，也可能因 package build ID 不一致
而无法加载。

当前 v2 发布约束：

```text
Bifrost transport baseline: transports/v2.0.0
Moon host branch:           moon/bifrost-v2.0.0
Rollback branch:            moon/bifrost-v1.6.10
Go:                         1.27.0
Target:                     linux/amd64, Debian/glibc
Dockerfile:                 transports/Dockerfile.dynamic-debian
```

这里的 Go 版本指 v2 各模块（`core/go.mod`、`framework/go.mod`、`transports/go.mod`）和
发布容器的构建要求。仓库根目录可能存在被 Git 忽略的旧 `go.work`（例如 v1.6 开发环境
留下的 1.26.x workspace）；它不是 v2 发布输入，不要修改或复用。跨模块验证应创建临时
Go 1.27 workspace，正式发布始终使用 Dockerfile 固定的 Go 1.27.0 工具链。

## 版本规则

镜像与插件使用同一个 `<官方版本>-moon.<迭代号>`，例如：

```text
2.0.0-moon.6
```

同一官方基线上的 Moon 修改只递增 `moon.N`。升级官方基线时更新前半部分并重新从 1
开始。发布清单必须记录宿主 commit、插件 commit、镜像 digest、插件 SHA-256、Go 版本、
架构和构建时间，不能只依赖文件名判断兼容性。

## 构建宿主镜像

在本仓库根目录执行：

```sh
RELEASE_VERSION=2.0.0-moon.6

docker build \
  --platform linux/amd64 \
  --build-arg VERSION="$RELEASE_VERSION" \
  -f transports/Dockerfile.dynamic-debian \
  -t "bifrost-moon:$RELEASE_VERSION" \
  .

docker save "bifrost-moon:$RELEASE_VERSION" \
  -o "bifrost-moon-$RELEASE_VERSION-amd64.tar"
```

Dockerfile 会验证主程序动态链接到 glibc。禁止为该镜像添加静态链接参数；静态宿主不
支持 Go 动态插件。

## v2 构建缓存约束

Moon v2 只使用一个缓存根目录，禁止再创建 `.gocache`、`.tmp-go-cache*` 或按
feature/race/review 命名的缓存：

```text
macOS: /private/tmp/moon-bifrost-v2-cache
Linux: ${TMPDIR:-/tmp}/moon-bifrost-v2-cache
```

宿主机 Go 命令通过统一包装器执行：

```sh
scripts/with-moon-v2-cache.sh go test ./core/... ./framework/... ./transports/...
scripts/with-moon-v2-cache.sh go test \
  ./plugins/governance/... ./plugins/logging/... ./plugins/otel/... ./plugins/routing/...
```

仓库根目录本身不是 Go module，因此不要使用根级 `go test ./...`；应显式列出需要测试的
module。包装器会自动发现 `plugins/*/go.mod` 并保证临时 workspace 覆盖全部生产插件模块。

包装器会在该根目录创建并复用一个只包含 v2 模块的 Go 1.27 workspace，并让宿主测试
使用 `go-build-host`；它不会修改仓库根目录遗留的 v1 `go.work`，也不会创建第二份 module
cache。插件构建脚本复用同一根目录中的 `go-mod` 和 `go-build-linux-amd64`，避免不同
GOOS/GOARCH 相互污染。可用 `BIFROST_V2_CACHE_ROOT` 覆盖根目录，但同一次升级期间必须
保持唯一值。

两个脚本默认在执行前检查该根目录不超过 8GiB；超限会拒绝继续，避免缓存静默增长。
仅在明确评估磁盘后才可通过 `BIFROST_V2_CACHE_MAX_GIB` 提高上限。

动态 Dockerfile 使用固定的 `moon-bifrost-v2-*` BuildKit cache ID。不要使用随机 cache
ID，也不要为每次构建创建新 builder。发布后检查 `docker buildx du`；当前 builder 超过
8GB 时执行：

```sh
docker buildx prune --max-used-space 8gb --reserved-space 2gb -f
```

该命令作用于当前 builder 的所有项目，应在确认没有其他构建正在运行后执行。它不会
删除已生成镜像。整个升级结束且不再需要增量编译时，可以删除唯一 v2 缓存根目录。

随后在相邻插件仓库使用同一个 `RELEASE_VERSION` 和当前 fork workspace 构建 `.so`：

```sh
cd ../bifrost-moon-plugin
scripts/build-fork-plugin.sh "$RELEASE_VERSION"
```

## 发布前验证

发布前至少完成：

1. 宿主和插件工作树只包含确认过的改动，本地 tar/cache 不进入 Git。
2. Go full/race/vet、UI typecheck/test/build 全部通过。
3. 在一次性数据库副本上执行 v2 migration，并验证旧数据可读、重复 migration 幂等。
4. 动态镜像启动后 `ldd /proc/1/exe` 包含 `libc.so.6`。
5. Moon `.so` 从容器内的新文件路径加载成功，无 package-version/build-ID 错误。
6. Chat、Responses、Image generation/edit/variation、fallback、streaming、治理与日志路径通过。

### 生产配置兼容审计

不要将生产密钥提交到仓库。先把生产 `config.json` 和部署/调用清单复制到受控临时目录，
再从仓库根目录运行只读审计：

```sh
cd transports
GOWORK=off go run ./cmd/moon-v2-audit \
  -config /secure/moon/config.json \
  -scan /secure/moon/deployment-manifests \
  -scan /secure/moon/caller-configs
```

命令会校验 v2 `config.schema.json`，检查动态插件和 SQLite 的容器路径、私网插件下载
allowlist、加密/初始化配置，并报告旧 `/api/governance/routing-rules`、旧复杂度路由路径和
`x-bf-prom-*` 调用。输出只包含 JSON 字段位置或文件名/行号，不打印配置值和源代码行。
`ERROR` 会返回非零退出码；`WARN` 必须在发布清单中逐项确认。
审计默认不发起 DNS 请求：远程插件使用主机名且未按精确 hostname allowlist 时会产生
`plugin-host-dns-unverified`。这不是放行结论；仍必须从候选容器执行实际下载/加载验证，
因为服务端会在每次拨号时重新解析 DNS，并拒绝任何未获 allowlist 许可的非公网地址。

## Canary 与回滚

v2 canary 必须使用独立端口和从生产数据复制出的独立目录。不要让 v1.6 和 v2 实例同时
写同一个数据库或 `/app/data`。建议按 1% → 10% → 50% → 100% 放量，并观察错误率、
超时、fallback 恢复率、延迟、成本、插件异常、Sidekiq 任务和数据库写入。

回滚时镜像、`.so`、配置和数据目录必须作为一个版本单元切回。`moon/bifrost-v1.6.10`
仍可部署，但只能配套其对应的 v1.6 插件和迁移前数据副本。新 `.so` 不能加载到旧宿主，
旧 `.so` 也不能加载到 v2 宿主。

未经 canary、迁移演练和成对 ABI 验证，不要将 `upstream/dev` 或新的上游 tag 直接部署到
生产维护分支。
