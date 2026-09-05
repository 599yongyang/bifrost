<!-- generated-by: gsd-doc-writer -->
# Moon 插件固定路径优化备忘录

## 状态

- 状态：待规划，当前不执行
- 记录日期：2026-09-05
- 当前发布期间：继续使用现有兼容入口
- 实施方式：独立维护窗口，不与普通版本升级混做

## 当前情况

生产库和测试库的 `config_plugins.path` 当前保存：

```text
/app/data/plugins/bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.18.so
```

这是容器内的固定逻辑入口，不代表实际仍运行 `.18`。`bifrost-deploy` 会根据 `versions.env`，把当前发布目录中的插件：

```text
/opt/bifrost/releases/<当前版本>/moon.so
```

挂载到上述入口。因此，实际版本以 `bifrost-deploy status`、挂载来源、`release.json` 和 SHA-256 为准。

当前方案功能正确，也没有查询或运行性能损失；主要问题是后台路径带 `.18`，容易让管理员误判插件没有升级。

## 目标状态

数据库改为保存不含版本号的稳定逻辑路径：

```text
/app/data/plugins/moon.so
```

宿主机仍按版本保存真实文件：

```text
/opt/bifrost/releases/2.0.0-moon.19/moon.so
/opt/bifrost/releases/2.0.0-moon.20/moon.so
```

目标关系：

```text
versions.env 选择版本
        ↓
releases/<版本>/moon.so
        ↓ 挂载
/app/data/plugins/moon.so
        ↓
config_plugins.path 固定引用
```

此次优化只调整逻辑入口名称，不修改插件名称 `moon`、插件功能、Provider/Key/路由、日志数据或 PostgreSQL 表结构。

## 为什么现在不改

`config_plugins.path` 位于共享数据库，修改后会同时影响使用该配置库的节点。当前版本更新尚未完成全部生产节点提升，因此不能在节点处于不同准备状态时切换共享路径。

还存在三个回退风险：

1. 只改数据库但某个容器没有 `/app/data/plugins/moon.so`，该节点重载或重启后会加载失败。
2. Go 插件同进程按新旧路径重复加载，不能假设热切换一定安全，应使用冷重建验证。
3. 旧部署快照只挂载 `.18` 兼容入口；数据库先改为 `moon.so` 后，直接回退旧快照可能缺少新入口。

因此路径优化必须使用“双入口过渡”，不能直接在后台编辑 Path。

## 启动实施的前提

同时满足以下条件后再排期：

- 本次候选版本已在 B 测试通过，并已部署到 A、C；三台使用同一已批准版本。
- A/C 实际请求、fallback、共享日志、身份隐藏和 Langfuse 已验收。
- 三台 `bifrost-deploy status` 均无 `PENDING` 操作。
- 生产配置库、测试配置库及部署文件已有可恢复备份。
- 已确认路径变更期间的 NewAPI 摘流、排空和节点处理顺序。
- 已定义路径迁移后的应用回退与数据库 Path 回退顺序。

任何一项不满足，都继续保留当前 `.18` 兼容入口。

## 计划实施步骤

### 第一阶段：工具支持双入口

更新 `bifrost-deploy`，让同一个实际插件文件同时挂载到：

```text
/app/data/plugins/bifrost-moon-response-sanitizer-linux-amd64-glibc-2.0.0-moon.18.so
/app/data/plugins/moon.so
```

此阶段数据库仍引用旧入口。为双挂载、Compose 解析、版本配套和回退增加测试，不先修改任何数据库。

### 第二阶段：所有节点预置新入口

1. 在 B 测试模式部署双入口版本。
2. 验证两个容器路径存在，SHA-256 均等于当前 `releases/<版本>/moon.so`。
3. 完成真实测试请求。
4. 让 B 切换备用并验收。
5. 按摘流顺序逐台部署 A、C 的双入口配置。

完成后，所有可能连接对应共享配置库的运行节点都必须具备新旧两个入口。

### 第三阶段：先迁测试库

备份测试配置库，确认 B 正在 test 模式且新入口已验证，再把测试库中 `moon` 插件 Path 改为：

```text
/app/data/plugins/moon.so
```

冷重建 B 测试模式，并验证插件 active、真实生图、fallback、日志和身份隐藏。失败时先把测试库 Path 恢复到旧入口，再恢复对应部署版本。

### 第四阶段：迁生产库

确认 A/C 以及可能加入生产的 B 备用都具备双入口后：

1. 创建生产配置库恢复点并记录旧 Path。
2. 低峰期执行，保留至少一台已验证生产节点承接。
3. 将生产配置库中的 `moon` Path 改为 `/app/data/plugins/moon.so`。
4. 逐台摘流、冷重建并验收 A、C，不同时重启。
5. 确认 NewAPI 流量、共享日志和 Langfuse 正常。

共享配置可能触发运行态重载，所以修改前必须已经在所有生产容器中预置新入口。

### 第五阶段：保留兼容入口

数据库切换成功后，暂时继续保留旧 `.18` 入口挂载。只有满足以下条件后，才能在另一个发布中删除旧入口：

- 新路径稳定运行超过批准的回退窗口；
- 所有可回退版本都包含 `moon.so` 入口，或已经不再允许回退到更旧版本；
- 部署文档、工具测试和恢复步骤已经更新；
- 已确认没有数据库、脚本或人工流程继续引用旧路径。

## 验收要求

每个环境至少核对：

```bash
sudo /opt/bifrost/bifrost-deploy status

sudo docker inspect bifrost --format \
  '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}' |
  grep moon

sudo docker exec bifrost sha256sum \
  /app/data/plugins/moon.so

sudo /opt/bifrost/bifrost-deploy verify
```

还要从相应配置库确认 `moon` 唯一启用且 Path 正确，并完成真实业务验收。仅看到后台路径变化、容器 healthy 或文件存在，均不足以判定完成。

## 回退原则

- 双入口阶段：数据库继续使用旧 Path，按普通部署回退。
- 测试库切换失败：先把测试库 Path 恢复旧值，再冷重建测试节点。
- 生产库切换失败：保持故障节点摘流，先把生产库 Path 恢复旧值，再逐台恢复已批准版本。
- 不在部分节点缺少目标入口时反复修改共享 Path。
- 不通过删除插件、清空配置库或恢复整库来替代单字段路径回退。

路径迁移完成后，再把本备忘录的状态改为“已完成”，记录实际版本、时间、配置库备份和验收请求 ID。
