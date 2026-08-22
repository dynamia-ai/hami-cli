# hami-cli

`hami-cli` 是 HAMi 的统一命令行入口。源码仓库保持私有，正式版本通过公开的 OSS 二进制下载和 GHCR 容器镜像分发。当前版本已从 `hami-enterprise/cmd/hami-cli` 迁入资源池管理能力；Docker 场景下的厂商运行时插件尚未实现。

## 当前能力

当前命令用于管理 HAMi Enterprise 的物理 GPU 资源池：

| 命令 | 作用 |
|---|---|
| `hami-cli pool list/create/delete` | 查看、创建或删除 `ResourcePool` |
| `hami-cli device list/assign/unassign` | 查看 GPU，或修改 GPU 与资源池的归属关系 |
| `hami-cli binding list` | 查看命名空间与资源池的绑定关系 |
| `hami-cli bind/unbind` | 授予或撤销命名空间的资源池访问权 |
| `hami-cli apply -f` | 从 YAML 或 JSON 应用资源池对象 |
| `hami-cli version` | 查看版本和构建信息 |

命令沿用标准 kubeconfig 加载规则，并支持 `--kubeconfig`、`--context`、`--namespace` 等 Kubernetes 客户端参数。

## 本地构建

前置条件：

- Go 1.26.0 或更高的 Go 1.26 补丁版本。
- 能够读取 `github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api` 私有 Go 模块。

执行以下命令：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off go build -o bin/hami-cli .
```

运行单元测试：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off go test ./...
```

## 安装公开版本

发布产物覆盖 Linux 和 macOS 的 amd64/arm64，以及 Windows amd64。稳定版本标签采用不可变的 `vX.Y.Z`，标签指向的提交必须已进入 `main`：

```text
https://storage.dynamia.ai/hami-cli/vVERSION/
```

例如，下载 Linux amd64 版本并校验：

```bash
VERSION=1.2.3
curl -fLO "https://storage.dynamia.ai/hami-cli/v${VERSION}/hami-cli_v${VERSION}_linux_amd64.tar.gz"
curl -fLO "https://storage.dynamia.ai/hami-cli/v${VERSION}/checksums.txt"
sha256sum --ignore-missing --check checksums.txt
tar -xzf "hami-cli_v${VERSION}_linux_amd64.tar.gz"
install -m 0755 "hami-cli_v${VERSION}_linux_amd64/hami-cli" /usr/local/bin/hami-cli
```

每次提交合入 `main` 后，workflow 还会覆盖发布 `latest` 版本。它适合持续集成验证，不提供稳定版本的不可变保证：

```bash
curl -fLO "https://storage.dynamia.ai/hami-cli/latest/hami-cli_latest_linux_amd64.tar.gz"
curl -fLO "https://storage.dynamia.ai/hami-cli/latest/checksums.txt"
sha256sum --ignore-missing --check checksums.txt
```

由于源码仓库是私有仓库，以下 `go install` 方式只适用于已获得仓库和私有依赖访问权限的开发者，不是公开安装渠道：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off \
  go install github.com/dynamia-ai/hami-cli@VERSION
```

将 `VERSION` 替换为已发布版本标签。

## Docker

容器镜像支持 `linux/amd64` 和 `linux/arm64`。首次创建 GHCR package 后，仓库管理员需要在 package 设置中取消继承私有仓库权限，并将可见性设为 `Public`；这是 GitHub 对新 package 的一次性默认限制。Release workflow 会以匿名身份验证镜像。镜像未公开时，workflow 会失败；完成设置后，只需重新运行失败的 job。之后可匿名拉取和运行：

```bash
docker run --rm ghcr.io/dynamia-ai/hami-cli:v1.2.3 version
```

跟随 `main` 的最新镜像使用 `latest` 标签：

```bash
docker run --rm ghcr.io/dynamia-ai/hami-cli:latest version
```

GHCR 不提供服务端不可变标签。稳定版 workflow 会将镜像 digest 写入公开的 `image-digest.txt`，并验证 `vX.Y.Z` 标签在发布时指向该 digest。需要不可变镜像引用时，使用 `ghcr.io/dynamia-ai/hami-cli@sha256:...`，不要只依赖标签。GHCR package 的写权限必须只授予本仓库 workflow；其他主体拥有写权限时，可能在 workflow 结束后修改标签。

需要访问 Kubernetes 集群时，将 kubeconfig 只读挂载到容器，使用宿主机 UID/GID 运行，并显式设置 `KUBECONFIG`。指定 UID/GID 后，Linux 上归宿主机用户所有且权限为 `0600` 的 kubeconfig 仍可读取：

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "$HOME/.kube:/kube:ro" \
  --env KUBECONFIG=/kube/config \
  ghcr.io/dynamia-ai/hami-cli:v1.2.3 pool list
```

kubeconfig 中的 API Server 地址必须能从容器网络访问；指向宿主机 `localhost` 的配置不能直接在容器内使用。当前 distroless 镜像不包含 `aws`、`gcloud`、`kubelogin` 等外部凭据插件，因此不能直接使用依赖 kubeconfig `exec` 插件的身份认证配置。

## 发布配置

Pull Request 和 `main` 分支使用同一组本地 `make ci`、容器构建和冒烟测试合同。GoReleaser 统一生成五个平台归档和 SHA-256 校验文件；Actions 只负责触发策略、凭据、对象存储、GHCR 和公开可用性验证。发布分为三个通道：

- 稳定标签 `vX.Y.Z`：向 OSS 和 S3 兼容存储发布不可变归档及容器镜像 digest，并创建仅授权用户可访问的 GitHub Release 副本。GHCR 同名标签是经过发布时校验的便捷引用，不属于不可变合同。
- `main` 每次更新：覆盖发布 OSS、S3 兼容存储和 GHCR 的 `latest`，并在该 workflow run 中保留名为 `release-binaries-latest` 的 Actions artifact。
- 手动调度：从 `main` 上运行 Rolling Release workflow，输入仓库中已有的远端分支。工作流将分支名归一化为 `branch-<name>`，覆盖发布同名对象存储目录、GHCR 标签和 Actions artifact。若原分支名包含 `/`、大写字符或其他不安全字符，版本末尾会附加短哈希；最终版本名会写入 job summary。

手动发布指定分支：

```bash
gh workflow run rolling-release.yaml --ref main -f branch=feature/my-change
```

稳定版 OSS、S3 兼容存储对象和 GitHub Release 资产不可覆盖；同一提交产生的相同内容可在失败后继续发布。OSS 上传使用服务端禁止覆盖参数；S3 上传使用 `If-None-Match: *` 条件请求。两个后端都会保存容器镜像 digest。若对象、GHCR 标签或 GitHub Release 资产已存在但内容、digest 或源码 revision 不同，workflow 会停止。`latest` 和分支版本是可变通道。所有通道都会构建五个平台归档、生成 SHA-256 校验文件并发布多架构 GHCR 镜像。只有稳定标签会创建 GitHub Release。

仓库需要配置以下 Actions secrets：

- `HAMI_ENTERPRISE_REPO_TOKEN`：对 `github.com/dynamia-ai/hami-enterprise` 具有只读 `Contents` 权限，用于下载私有 Go 模块。
- `ALIYUN_OSS_ACCESS_KEY_ID`、`ALIYUN_OSS_ACCESS_KEY_SECRET`：OSS 访问凭据。
- `ALIYUN_OSS_BUCKET`、`ALIYUN_OSS_ENDPOINT`、`ALIYUN_OSS_REGION`：OSS Bucket 名称、原生 API 端点和区域。工作流不推导默认端点，这三个 Secret 均为必填项。对应 RAM 身份需要读取 Bucket 版本控制状态，读取和写入对象，并能设置对象 ACL。发布 Bucket 必须关闭版本控制；版本控制处于 `Enabled` 或 `Suspended` 状态时，workflow 会停止。
- `S3_ACCESS_KEY_ID`、`S3_SECRET_ACCESS_KEY`：S3 兼容存储访问凭据。
- `S3_BUCKET`、`S3_ENDPOINT`、`S3_REGION`：S3 兼容存储的 Bucket 名称、API 端点和区域。使用 Cloudflare R2 时，`S3_ENDPOINT` 为 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`，`S3_REGION` 为 `auto`。R2 API Token 至少需要目标 Bucket 的 Object Read & Write 权限。

仓库还需要配置以下 Actions variables：

- `ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL`：OSS 公开下载前缀。默认值为 `https://storage.dynamia.ai/hami-cli`。
- `S3_PUBLIC_DOWNLOAD_BASE_URL`：S3 兼容存储公开下载前缀，无默认值。使用 R2 时，需要先为 Bucket 启用公开自定义域名或 `r2.dev` 地址，再将包含 `/hami-cli` 的公开前缀写入该变量。R2 不支持 S3 对象 ACL，发布脚本不会设置 `public-read`。

由于同仓库 Pull Request workflow 会向受提交控制的构建步骤提供私有模块 token，`hami-cli` 的 write 权限不得授予无权读取 `hami-enterprise` 的用户。每次调整任一仓库的成员权限时都必须重新核对这一不变量；如果两个仓库的写入和读取边界不再一致，应先停用 Pull Request 中需要 token 的 job，改用具备审批保护的凭据边界后再授予权限。

工作流会从两个公开下载前缀逐个下载并比对上传结果，避免把对象上传成功误当成用户已可下载。任一后端上传或公开验证失败时，发布任务都会停止。

## 厂商插件方向

项目只发布一个用户侧 `hami-cli` 二进制。NVIDIA、Ascend 等厂商适配将作为编译进同一二进制的厂商插件接入，不为每个厂商维护独立 CLI。

厂商插件负责设备发现、宿主机前置检查、Core 定位、Docker 参数注入和临时状态清理。主程序负责统一命令合同、参数分隔、`--dry-run` 输出、进程退出码和信号处理。

首个厂商插件开始实现前，需要先根据已经验证的 NVIDIA Docker PoC 固定插件合同。当前迁移不包含 Docker 参数注入，也不声明 NVIDIA 或 Ascend 已受支持。详细边界见 [架构说明](docs/architecture.md)。

## 迁移说明

当前资源池实现来自 `hami-enterprise` 的 `epic/resource-pool-manager` 分支。迁移阶段保留原命令、输出和并发更新语义，并继续依赖独立发布的 `pkg/resourcepool/api` 模块。

从 `hami-enterprise` 删除旧目录、切换发布流水线和发布首个独立版本属于后续工作。完成这些操作前，需要先验证新仓库的独立构建、安装和发布流程。

## License

本项目使用 [Apache License 2.0](LICENSE)。
