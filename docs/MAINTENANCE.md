# Moon 分支维护指南

本分支是 Moon 生产环境使用的 Bifrost 宿主程序，当前以官方 `transports/v1.6.10`
版本线为基础，并包含 Moon 生产环境所需的本地定制。相关改动会随业务持续迭代，因此
本文档不维护具体改动清单，只记录版本规则、构建要求、部署方式和上游升级流程。

与 Moon 插件相关的构建和发布说明位于相邻的 `bifrost-moon-plugin` 仓库中。

## 本地目录要求

```text
project/
├── bifrost/
└── bifrost-moon-plugin/
```

执行生产发布时，这两个仓库必须位于同一工作目录下。Go 插件要求宿主程序和插件针对
`core/schemas` 及其他共享包使用完全一致的 Go 构建标识。

## 版本命名规则

Moon 镜像版本采用 `<Bifrost 官方版本>-moon.<Moon 本地迭代版本>` 格式。例如：

```text
bifrost-moon:1.6.10-moon.21
               │       │
               │       └── Moon 本地修改后的第 21 个迭代版本
               └────────── Bifrost 官方基线版本 1.6.10
```

因此，`1.6.10-moon.21` 并不是 Bifrost 官方发布的完整版本号：其中 `1.6.10` 表示当前采用的
Bifrost 官方版本，`moon.21` 表示基于该官方版本进行本地修改后的第 21 个 Moon 版本。

升级 Bifrost 官方基线版本时，更新前半部分（例如从 `1.6.10` 更新为 `1.6.11`）；在同一
官方基线上继续修改 Moon 定制逻辑时，只递增 `moon.N`。

## 构建与发布

配套构建、灰度验证、完整校验及回滚操作的权威说明位于相邻仓库：
`../bifrost-moon-plugin/docs/RELEASE.md`。

在本仓库目录中构建镜像。以下示例使用当前生产环境的 Moon 本地第 21 个迭代版本：

```sh
docker build \
  --platform linux/amd64 \
  --build-arg VERSION=1.6.10-moon.21 \
  -f transports/Dockerfile.dynamic-debian \
  -t bifrost-moon:1.6.10-moon.21 \
  .
```

将镜像导出为可供服务器加载的归档文件：

```sh
docker save bifrost-moon:1.6.10-moon.21 \
  -o ./bifrost-moon-1.6.10-moon.21-amd64.tar
```

随后必须使用完全相同的 `core + framework + transports + plugin` workspace、Go 版本、
CPU 架构以及 Debian/glibc 环境构建配套的 Moon 插件。升级、部署和回滚镜像时，必须同步
处理对应的 `.so` 插件。

## 生产环境部署

当前生产环境使用的镜像版本为 `bifrost-moon:1.6.10-moon.21`，即基于 Bifrost 官方
`1.6.10` 版本的 Moon 本地第 21 个迭代版本。先将镜像归档文件复制到服务器，进入归档
文件所在目录，然后依次执行以下命令：

```sh
sudo docker load -i bifrost-moon-1.6.10-moon.21-amd64.tar

sudo docker stop bifrost
sudo docker rm bifrost

sudo docker run -d \
  --name bifrost \
  --restart unless-stopped \
  --add-host langfuse-archive.tailb34b09.ts.net:100.108.96.112 \
  -p 8080:8080 \
  -v /opt/bifrost-data:/app/data \
  bifrost-moon:1.6.10-moon.21
```

该部署命令会将 Bifrost 数据持久化到宿主机的 `/opt/bifrost-data` 目录，通过 `8080`
端口对外提供网关服务，并在容器内添加生产环境使用的 Langfuse Archive 主机名映射。

## 安全升级上游版本

升级到新的 Bifrost 官方版本时：

1. 从新的上游 transport 标签创建 Moon 维护分支。
2. 逐项迁移并核对 Moon 本地改动，解决与新上游版本之间的冲突。
3. 运行相关测试，并使用 `transports/Dockerfile.dynamic-debian` 构建动态链接镜像。
4. 使用相邻仓库中的发布脚本重新构建配套的 Moon 插件。
5. 在切换任何生产流量之前，先完成灰度环境验证。

未经上述验证，不要将 `upstream/dev` 直接合并到生产维护分支。
