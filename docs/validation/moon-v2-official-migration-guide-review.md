# Moon v2 官方迁移指南审阅

审阅日期：2026-08-29

官方参考：
- https://docs.getbifrost.ai/migration-guides/v2.0.0
- https://docs.getbifrost.ai/changelogs/v2.0.0

本文件只记录 v2.0.0 官方迁移要求与当前 Moon 分支的对照，不代表生产发布批准。

## 当前生产现场结论

- 现场插件路径是 `/app/data/plugins/...so`，属于本地挂载路径，因此官方 Breaking Change 1
  的 HTTP(S) 私网下载 allowlist 当前不适用；只有以后改成内网 URL 下载时才需要配置。
- 现场 v2 曾加载 `1.6.10-moon.31.so` 并触发 Go runtime fatal。`2.0.0-moon.3` 已增加
  `plugin.Open` 前的 Go 版本/平台/buildmode 预检：旧插件记为 `error`，基础网关继续启动，
  但完整 Moon 能力仍要求换成匹配的 moon.3 `.so`。
- 通过 UI/API 更新自定义插件路径前必须确认 dashboard 管理员认证可用；这是官方 Breaking
  Change 2 的直接影响。未配置真实管理员认证时，应通过受控的部署配置流程更新路径。
- 现场把 `/opt/bifrost-data` 直接挂载给 v2，且启动已经进入数据库初始化和插件加载阶段。
  该目录必须按 v2 数据对待，不能再直接交给 v1.6；v1 回滚只能使用迁移前快照。

## 结论先说

这次 v2.0.0 的升级重点不在“新功能多不多”，而在 6 个会影响上线方式的变更：

1. 自定义插件下载改成 SSRF 保护，私网/环回/链路本地地址默认会被拦。
2. 自定义路径插件的创建/更新，只有真正的管理员认证才能通过。
3. 治理类 API 迁到 `/api/governance/*`，路由类 API 迁到 `/api/routing/*`，旧路径只保留兼容窗口。
4. `HTTPTransportPreHook` 的时序变了，真正需要在认证前做事的逻辑要搬到 `HTTPTransportPreAuthHook`。
5. `cost` 从单值/扁平结构改成 input/output/additional 三段式。
6. 观测字段的旧别名被移除，仪表盘、告警和查询要切到 canonical key。

对 Moon 来说，代码仓库里这些点大部分都已经有对应实现或文档，但是否“可以直接切换”，还取决于你的生产配置、插件路径、外部 API 调用方和监控查询。

## 对照表

| 项目 | 官方要求 | Moon 当前状态 | 结论 | 需要你注意什么 |
| --- | --- | --- | --- | --- |
| 自定义插件下载 SSRF 保护 | 只允许公网地址；私网/环回/CGNAT/link-local 要写入 `server.plugin_download_private_allowlist` 或改成本地文件路径 | 已覆盖。`core/network/ssrf.go` 有 allowlist 专用路径，`transports/config.schema.json` 和 `moon-v2-audit` 也在校验该字段 | 已覆盖，但生产配置必须复核 | 如果你的 `.so` 还放在内网 artifact server 或 `localhost`，必须补 allowlist，否则 v2 起不稳定甚至直接失败 |
| 自定义路径插件的管理员认证 | `POST /api/plugins` 和 `PUT /api/plugins/{name}` 在设置 `path` 时要求真实管理员认证 | 已覆盖。`transports/bifrost-http/handlers/plugins.go` 已明确拒绝“dashboard auth 被禁用/未配置”时的自定义 path 写入 | 已覆盖 | 这会影响你用 API 动态改插件路径的流程；启动后再改 path 也不再是无感操作 |
| 治理 API 路径迁移 | 迁到 `/api/governance/*`；Team/User 列表改为 `limit`/`offset`；旧路径保留兼容窗口 | 已覆盖。`transports/bifrost-http/handlers/routing.go` 仍同时挂载 `/api/routing/*` 和旧 `/api/governance/*`；`docs/api/procuring-api-keys.mdx` 也已经列出 canonical/legacy 两套路径 | 部分已覆盖，外部调用方仍要迁移 | 你自己的脚本、SDK 调用、Postman、前端代码要尽快切到 `/api/routing/*` 和 `/api/governance/*` 的 canonical 版本，不要依赖兼容窗口 |
| `HTTPTransportPreAuthHook` / `HTTPTransportPreHook` 时序 | 需要在认证前拿/改凭证的逻辑必须放进 `PreAuthHook`；`PreHook` 现在在认证后运行 | 已覆盖。`core/schemas/plugin.go` 已把执行顺序和责任边界写清楚；`plugins/logging/main.go` 也实现了 `HTTPTransportPreAuthHook` | 已覆盖，但自定义插件要重编 | 这是这次最容易漏的点之一：凡是从 `x-bf-vk`、`Authorization`、上游身份头派生认证信息的插件，都要检查是不是应该迁到 `PreAuthHook` |
| `cost` 结构拆分 | `input_cost` / `output_cost` / `additional_cost` 取代只看 total 的旧思路 | 已覆盖。`core/schemas/chatcompletions.go` 定义了三段式 `BifrostCost`，`framework/logstore/tables.go` 也已把三列作为 UI/回算依据 | 已覆盖，但外部消费方要同步 | 如果你有日志导出、BI、统计脚本，凡是还在把 `cost` 当成纯 scalar 或扁平字段读写的，都要改 |
| 旧观测别名移除 | 仪表盘、告警、查询要从旧别名切到 canonical 的 `bifrost.*` 或 OTel 标准键 | 部分覆盖。仓库里已有相关改动和说明，但这类问题常常藏在外部 dashboard / alert rule / query 里 | 部分覆盖 | 这不是代码能完全兜住的，最容易在“接口已经通了，但监控全空了”的时候才暴露 |
| Runware Video costing 限制 | 官方 changelog 说明：视频任务的 provider cost 不能回写到原生成日志 | Moon 仓库没有显示出额外补丁能完全消除这个限制 | 与 Moon 业务有关但不是迁移阻断 | 如果你依赖 Runware Video 成本统计，这块还是要人工核对，不要假设 v2 会自动补齐 |

## Moon 里已经很明确的覆盖点

- 插件下载的私网 allowlist 已经进入代码、schema 和审计命令，说明这次升级不是“只换版本号”，而是要把部署环境一起补齐。
- 插件创建/更新的认证边界已经在 HTTP handler 里落地，说明 Moon 这边不会再把“未认证的自定义 path 写入”当成正常流程。
- 路由 API 双路径兼容还在，说明切换期内旧 `/api/governance/routing-rules` 还能跑，但官方已经明确把 `/api/routing/*` 作为新入口。
- `BifrostCost` 和 logstore 的拆分已经是正式结构，不是临时兼容字段。
- `HTTPTransportPreAuthHook` 已经在接口和实现里出现，说明插件 ABI 已经进入新阶段。

## 我建议你优先处理的事情

### 1. 先查插件路径

如果你的生产插件 `.so` 来自：

- 内网 artifact server
- 只在容器内可见的地址
- `localhost` / `127.0.0.1`
- link-local 或私网 IP

那就先补 `server.plugin_download_private_allowlist`，或者直接改成挂载本地文件路径。

官方迁移页已经明确说了，这种下载在 v2.0.0 之后是会被拦的。

### 2. 再查自定义插件的认证方式

如果你们当前有“dashboard auth 关着也能改插件 path”的流程，这在 v2.0.0 里要改掉。

这不是小修小补，是行为边界变了。

### 3. 然后查所有治理/路由调用方

重点扫这些老路径：

- `/api/governance/routing-rules`
- `/api/governance/complexity-analyzer-config`
- `/api/teams`
- `/api/users`
- `/api/roles`
- `/api/audit-logs`

官方现在允许有一段兼容窗口，但新集成应该尽快迁到 canonical 路径。

### 4. 最后查监控和报表

如果你的 Grafana / Prometheus / OTel 查询还在看旧的观测别名，升级后很可能“服务没坏，面板先坏”。

这类问题最容易被忽略，因为 API 测试通常会先过，但观测系统会慢半拍。

## Moon 侧的运维约束也要一起保住

`docs/MAINTENANCE.md` 已经把 Moon v2 的边界写得很清楚：

- v1.6.10 和 v2.0.0 的镜像、数据库副本、Go 插件不能混用。
- v2 只保留一个缓存根目录，不要再维护 `.gocache`、`.tmp-go-cache*` 之类的多版本缓存。
- 发布前要保留 v1.6 回滚镜像、插件、配置和迁移前数据副本。

也就是说，官方迁移指南解决的是“功能怎么改”；Moon 维护指南解决的是“怎么安全上线和回滚”。

## 性能上有没有明显优化

有，但它不是这次升级最该先看的点。

官方 changelog 提到两类比较实在的优化：

- 热路径 JSON 序列化更省分配
- 请求路径上的 allocation / tracing 开销下降

这类优化对高 RPS 场景是有价值的，但它们更像“升级后的收益”，不是“必须改完才能上”的阻断项。

如果你现在最关心的是能不能切 v2，我会把优先级排成：

1. 插件下载 allowlist
2. 自定义插件管理员认证
3. API 路径迁移
4. 监控查询和报表别名
5. `cost` 结构和下游消费方
6. 再看性能收益

## 总体判断

如果你们的 Moon 生产环境已经把插件路径、路由调用方和监控查询都清干净了，那么这次升级可以做，而且仓库里已经有比较完整的 v2 兼容基础。

如果上面任意一项还没盘清，我不建议直接把 v2 当成“只要编译过就能上”的升级。

最稳妥的做法还是：

1. 先在独立数据副本上跑迁移演练。
2. 再确认插件 `.so`、宿主二进制和缓存根目录都切到 v2 约束。
3. 最后按 canary 放量。

### 相关本地证据

- [docs/MAINTENANCE.md](../MAINTENANCE.md)
- [docs/MIGRATION-VALIDATION.md](../MIGRATION-VALIDATION.md)
- [core/network/ssrf.go](../../core/network/ssrf.go)
- [core/schemas/plugin.go](../../core/schemas/plugin.go)
- [core/schemas/chatcompletions.go](../../core/schemas/chatcompletions.go)
- [framework/logstore/tables.go](../../framework/logstore/tables.go)
- [docs/api/procuring-api-keys.mdx](../api/procuring-api-keys.mdx)
- [core/changelog.md](../../core/changelog.md)
