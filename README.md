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

正式版本标签采用不可变的 `vX.Y.Z`。标签指向的提交必须已进入 `main`。发布产物覆盖 Linux 和 macOS 的 amd64/arm64，以及 Windows amd64：

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

Pull Request 和 `main` 分支使用同一组本地 `make ci`、容器构建和冒烟测试合同。推送稳定版本标签后，Release workflow 会构建五个平台归档、生成 SHA-256 校验文件、上传到 OSS、创建仅授权用户可访问的 GitHub Release 副本，并发布多架构 GHCR 镜像。

仓库需要配置以下 Actions secrets：

- `HAMI_ENTERPRISE_REPO_TOKEN`：对 `github.com/dynamia-ai/hami-enterprise` 具有只读 `Contents` 权限，用于下载私有 Go 模块。
- `ALIYUN_OSS_ACCESS_KEY_ID`、`ALIYUN_OSS_ACCESS_KEY_SECRET`、`ALIYUN_OSS_BUCKET` 和 `ALIYUN_OSS_REGION`：访问发布前缀。对应 RAM 身份需要列举、读取和写入对象，并能设置对象 ACL。
- `ALIYUN_OSS_ENDPOINT`：可选；未配置时使用 `https://oss-${ALIYUN_OSS_REGION}.aliyuncs.com`。

由于同仓库 Pull Request workflow 会向受提交控制的构建步骤提供私有模块 token，`hami-cli` 的 write 权限不得授予无权读取 `hami-enterprise` 的用户。每次调整任一仓库的成员权限时都必须重新核对这一不变量；如果两个仓库的写入和读取边界不再一致，应先停用 Pull Request 中需要 token 的 job，改用具备审批保护的凭据边界后再授予权限。

如公开下载域名不是 `https://storage.dynamia.ai/hami-cli`，设置 Actions variable `HAMI_CLI_DOWNLOAD_BASE_URL`。工作流会在创建 GitHub Release 前从公开域名逐个下载并比对上传结果，避免把“OSS 上传成功”误当成“用户已可下载”。

## 厂商插件方向

项目只发布一个用户侧 `hami-cli` 二进制。NVIDIA、Ascend 等厂商适配将作为编译进同一二进制的厂商插件接入，不为每个厂商维护独立 CLI。

厂商插件负责设备发现、宿主机前置检查、Core 定位、Docker 参数注入和临时状态清理。主程序负责统一命令合同、参数分隔、`--dry-run` 输出、进程退出码和信号处理。

首个厂商插件开始实现前，需要先根据已经验证的 NVIDIA Docker PoC 固定插件合同。当前迁移不包含 Docker 参数注入，也不声明 NVIDIA 或 Ascend 已受支持。详细边界见 [架构说明](docs/architecture.md)。

## 迁移说明

当前资源池实现来自 `hami-enterprise` 的 `epic/resource-pool-manager` 分支。迁移阶段保留原命令、输出和并发更新语义，并继续依赖独立发布的 `pkg/resourcepool/api` 模块。

从 `hami-enterprise` 删除旧目录、切换发布流水线和发布首个独立版本属于后续工作。完成这些操作前，需要先验证新仓库的独立构建、安装和发布流程。

## License

本项目使用 [Apache License 2.0](LICENSE)。
