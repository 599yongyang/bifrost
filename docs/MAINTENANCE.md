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

## Canary 与回滚

v2 canary 必须使用独立端口和从生产数据复制出的独立目录。不要让 v1.6 和 v2 实例同时
写同一个数据库或 `/app/data`。建议按 1% → 10% → 50% → 100% 放量，并观察错误率、
超时、fallback 恢复率、延迟、成本、插件异常、Sidekiq 任务和数据库写入。

回滚时镜像、`.so`、配置和数据目录必须作为一个版本单元切回。`moon/bifrost-v1.6.10`
仍可部署，但只能配套其对应的 v1.6 插件和迁移前数据副本。新 `.so` 不能加载到旧宿主，
旧 `.so` 也不能加载到 v2 宿主。

未经 canary、迁移演练和成对 ABI 验证，不要将 `upstream/dev` 或新的上游 tag 直接部署到
生产维护分支。
