<!-- generated-by: gsd-doc-writer -->
# 新 Bifrost 服务器部署

适用于**没有旧 Bifrost 容器和旧数据目录**的新服务器。现有三台不要执行本流程；日常更新看 [bifrost-deploy 操作手册](BIFROST-DEPLOY.md)。

当前工具固定使用共享 PostgreSQL `10.1.12.7`、Moon 固定插件入口和 Langfuse 地址，是本项目的部署工具，不是通用 Bifrost 安装器。

## 1. 选择节点类型

| 类型 | 命令参数 | 角色 | 端口 / 数据库 |
|---|---|---|---|
| 正式节点 | `production` | 仅生产 | 8080 / `bf_prod` |
| 测试兼备用节点 | `test-standby` | test、standby 二选一运行 | 18080 / `bf_test`；8080 / `bf_prod` |

标准容器限制为 3 核、6GiB 内存，建议服务器至少 4 核 / 8GB。磁盘按本地运行数据和发布包保留量规划；请求日志仍写共享 PostgreSQL。

## 2. 部署前准备

先确定新服务器内网 IP 和用途，并在数据库服务器完成：

- 云安全组仅允许新节点到 `10.1.12.7:5432`；
- `pg_hba.conf` 按精确 `/32` 来源授权 `bf_prod`，测试/备用节点还需授权 `bf_test`；
- 数据库与表结构已经存在，Moon 插件记录已启用且路径仍为固定入口；
- 新节点能通过内网访问 PostgreSQL。

不要把 5432 开放到公网。正式/备用节点还要安装 Tailscale、加入正确 Tailnet，并确认能访问 `100.108.96.112`；测试模式默认不启用 OTel。

服务器需要 Ubuntu 24.04 AMD64、Docker/Compose、Python 3.11+ 和 PostgreSQL 16 客户端：

```bash
uname -m
docker --version
docker compose version
python3 --version
psql --version
ip -4 -brief address show scope global
```

Docker Hub 不可用时使用本手册的离线发布包，不临时改用来源不明的镜像站。

## 3. 安装部署工具

上传仓库中的 `scripts/deployment/bifrost-deploy`，然后：

```bash
sudo install -d -o root -g root -m 755 /opt/bifrost

sudo install -o root -g root -m 755 \
  /home/ubuntu/bifrost-deploy \
  /opt/bifrost/bifrost-deploy

sudo /opt/bifrost/bifrost-deploy --help
```

校验上传文件的 SHA-256，必须与本次经过测试的仓库版本一致；不要长期运行来历不明或被修改的脚本。

## 4. 初始化节点

### 正式节点

```bash
sudo /opt/bifrost/bifrost-deploy bootstrap production \
  --ip 10.0.0.10 \
  --version 2.0.0-moon.19
```

将示例 IP 和版本替换为新服务器实际值。工具交互读取并复核 `bf_prod` 密码，密码不会放入命令行。

### 测试兼备用节点

```bash
sudo /opt/bifrost/bifrost-deploy bootstrap test-standby \
  --ip 10.0.0.11 \
  --version 2.0.0-moon.19
```

工具依次读取 `bf_test`、`bf_prod` 密码并创建两套独立配置；各角色数据目录在第一次 `launch/deploy` 时分别创建。两个角色不能同时运行。

`bootstrap` 会验证 IP 属于本机、数据库身份/TLS、日志表、Moon 固定入口，以及测试 OTel 为关闭状态；它不会启动容器、修改数据库或加入 NewAPI。任一检查失败都先修正依赖，不绕过验证。

## 5. 导入发布包

在构建机按 [构建与打包](../DEPLOYMENT.md#artifacts) 生成配套发布目录。初始化成功后，开放专用上传目录：

```bash
sudo install -d -o ubuntu -g ubuntu -m 700 /opt/bifrost/inbox
```

将完整发布目录上传到 `inbox/`，再导入：

```bash
sudo /opt/bifrost/bifrost-deploy import \
  /opt/bifrost/inbox/bifrost-release-2.0.0-moon.19
```

导入会验证 `release.json`、镜像归档和插件哈希，并拒绝同版本不同内容；不会启动服务。

## 6. 首次启动

先执行只读预检：

```bash
# 正式节点
sudo /opt/bifrost/bifrost-deploy plan production

# 测试/备用节点首次通常启动 test
sudo /opt/bifrost/bifrost-deploy plan test
```

确认目标 IP、角色、版本、数据库、端口和插件哈希正确后：

```bash
# 二选一
sudo /opt/bifrost/bifrost-deploy launch production
sudo /opt/bifrost/bifrost-deploy launch test
```

确认文字为 `LAUNCH <角色> <版本>`。`launch` 只用于首次启动；节点有运行状态后改用 `deploy`。

如果首次启动失败，保持渠道未加入，查看：

```bash
sudo /opt/bifrost/bifrost-deploy status
sudo /opt/bifrost/bifrost-deploy rollback
```

首次回退会停止并保留已经创建的失败容器；如果 Compose 尚未创建容器，则只清理运行状态并保留诊断资料。它不会恢复或修改数据库。

## 7. 验收和加入流量

基础检查：

```bash
sudo /opt/bifrost/bifrost-deploy status
sudo /opt/bifrost/bifrost-deploy verify
```

还必须定向验证真实生图/编辑、尺寸、多图、fallback、共享日志、图片结果、身份隐藏，以及正式/备用的 Langfuse Trace 和媒体。观察请求期间的 CPU、内存、I/O、延迟和错误率。

正式节点验收通过后，才在 NewAPI 新增内部渠道并从低权重开始观察。测试端口 18080 不加入生产渠道。新增节点不自动提高数据库/NewAPI 可用性，数据库备份与容量仍按 [部署手册](../DEPLOYMENT.md) 管理。
