# hami-cli

`hami-cli` 用于管理 HAMi Enterprise 的物理 GPU 资源池。它通过 Kubernetes API 查看资源池和 GPU 清单、调整 GPU 归属，以及管理命名空间对资源池的访问权。当前资源池 PoC 只支持 NVIDIA 物理 GPU。

当前版本只提供 Kubernetes 资源池管理能力，不安装 HAMi Enterprise、资源池控制器或 CRD。Docker 场景的 NVIDIA、Ascend 等厂商运行时插件仍处于设计阶段，尚无可用的 `run` 命令。

## 安装

### 公开二进制

公开归档覆盖以下平台：

| 操作系统 | 架构 | 归档后缀 |
| --- | --- | --- |
| Linux | x86-64 | `linux_amd64.tar.gz` |
| Linux | ARM64 | `linux_arm64.tar.gz` |
| macOS | Intel | `darwin_amd64.tar.gz` |
| macOS | Apple silicon | `darwin_arm64.tar.gz` |
| Windows | x86-64 | `windows_amd64.tar.gz` |

以下命令安装随 `main` 更新的 `latest` Linux amd64 版本：

```bash
VERSION=latest
PLATFORM=linux_amd64
BASE_URL=https://storage.dynamia.ai/hami-cli
ARCHIVE="hami-cli_${VERSION}_${PLATFORM}.tar.gz"

curl -fLO "$BASE_URL/$VERSION/$ARCHIVE"
curl -fLO "$BASE_URL/$VERSION/$ARCHIVE.sha256"
test "$(sha256sum "$ARCHIVE" | cut -d ' ' -f1)" = "$(cat "$ARCHIVE.sha256")"
tar -xzf "$ARCHIVE"
sudo install -m 0755 "hami-cli_${VERSION}_${PLATFORM}/hami-cli" /usr/local/bin/hami-cli
hami-cli version
```

macOS 可将校验命令替换为：

```bash
test "$(shasum -a 256 "$ARCHIVE" | cut -d ' ' -f1)" = "$(cat "$ARCHIVE.sha256")"
```

每个归档都有同名的 `.sha256` 校验文件，用于检查下载内容是否完整。归档和校验文件来自同一个公开发布通道，因此该校验不能替代独立的制品签名或来源证明。

`latest` 是可变版本，适合验证和持续集成，不提供不可变保证。稳定版发布后，将 `VERSION` 替换为完整标签，例如 `vX.Y.Z`。目录名和归档文件名都保留前导 `v`。

公开文件同时发布到两个下载前缀。一个地址不可用时，可切换到另一个地址，文件名和目录结构不变：

- `https://storage.dynamia.ai/hami-cli`
- `https://public.hami.run/hami-cli`

### `go install`

源码仓库及资源池 API 依赖均为私有模块。只有同时拥有两个仓库读取权限的开发者才能使用 `go install`：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off \
  go install github.com/dynamia-ai/hami-cli@VERSION
```

将 `VERSION` 替换为已发布标签或其他可解析的 Go 模块版本。公开用户应使用预编译归档。

### 安装 Agent Skill

发布的 `hami-cli` 二进制内置与当前版本匹配的资源池管理 Skill。安装二进制后，运行以下命令，将 Skill 安装到本机已检测到的 Agent：

```bash
hami-cli skill install
```

非交互场景需要显式指定作用域和 Agent。例如，将 Skill 安装到 Codex 用户目录：

```bash
hami-cli skill install --scope user --agent codex --yes
```

Codex 用户作用域支持 `~/.agents/skills` 和兼容目录 `~/.codex/skills`。安装器优先复用已存在的兼容目录；两个目录都不存在时，使用 `~/.agents/skills`。项目作用域安装到当前项目的 `.agents/skills/managing-hami-resource-pools`：

```bash
hami-cli skill install --scope project --agent codex --yes
```

使用 `--dry-run` 可以在写入文件前查看安装计划。安装器会写入 `.kitup.json` 所有权元数据；目标目录存在但不属于 `hami-cli` 时，默认报告冲突，不覆盖已有内容。

## 连接集群

使用资源池命令前，确认以下条件：

- 集群已安装 HAMi Enterprise 资源池控制器及 `resource.hami.io/v1alpha1` CRD。
- kubeconfig 指向目标集群，并包含待操作资源所需的 RBAC 权限。
- 本地能够访问 kubeconfig 中的 API Server 和身份认证程序。

先确认当前上下文，再检查 CLI 能否读取资源池对象：

```bash
kubectl config current-context
hami-cli version
hami-cli pool list
hami-cli device list
hami-cli binding list
```

如果集群尚未安装资源池 CRD，命令会返回资源类型不存在，而不是自动修改集群。

## 常用操作

### 命令一览

| 命令 | 作用 |
| --- | --- |
| `hami-cli pool list/create/delete` | 查看、创建或删除 `ResourcePool` |
| `hami-cli device list/assign/unassign` | 查看物理 GPU，或修改 GPU 的资源池归属 |
| `hami-cli binding list` | 查看命名空间与资源池的绑定关系 |
| `hami-cli bind/unbind` | 授予或撤销命名空间对资源池的访问权 |
| `hami-cli apply -f` | 从 YAML、JSON 或标准输入应用资源池对象 |
| `hami-cli skill install` | 将二进制内置的资源池管理 Skill 安装到本地 Agent |
| `hami-cli version` | 查看版本、源码 revision、资源池 API revision、构建时间和目标平台 |
| `hami-cli completion` | 生成 Bash、Zsh、Fish 或 PowerShell 补全脚本 |

### 查看资源池、设备和绑定

```bash
# 查看资源池及已发现、健康的 GPU 数量
hami-cli pool list

# 查看全部物理 GPU，或只查看指定节点
hami-cli device list
hami-cli device list --node worker-gpu-01

# 查看当前命名空间或全部命名空间的绑定
hami-cli binding list
hami-cli binding list --all-namespaces
```

`pool list`、`device list` 和 `binding list` 支持 `table`、`name`、`json`、`yaml` 四种输出格式：

```bash
hami-cli pool list -o name
hami-cli device list -o yaml
hami-cli binding list -A -o json
```

### 创建资源池并分配 GPU

先创建空资源池，再从 `device list` 输出中选择设备：

```bash
hami-cli pool create training
hami-cli device list --node worker-gpu-01
```

可以使用 `GPUDevice` 对象名分配设备：

```bash
hami-cli device assign GPUDEVICE_NAME --pool training
```

对于 NVIDIA 设备，也可以使用节点名和物理 GPU UUID。两个参数必须同时提供；该快捷方式会按 NVIDIA 设备类型计算 `GPUDevice` 对象名：

```bash
hami-cli device assign \
  --node worker-gpu-01 \
  --device-id GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee \
  --pool training
```

移除设备归属：

```bash
hami-cli device unassign GPUDEVICE_NAME
hami-cli device unassign \
  --node worker-gpu-01 \
  --device-id GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
```

在共享集群中清理临时设备归属时，同时指定操作前记录的设备 UID 和资源池名称：

```bash
hami-cli device unassign GPUDEVICE_NAME \
  --expected-uid DEVICE_UID \
  --expected-pool training
```

`--expected-uid` 和 `--expected-pool` 必须同时使用。当前 UID 或资源池名称不符合预期时，命令停止且不修改设备。设备归属更新还带有资源版本前置条件；对象在命令读取后被其他客户端并发修改时，命令返回冲突，不会覆盖更新后的状态。

删除资源池前，需要先移除设备归属和其他引用：

```bash
hami-cli pool delete training
```

### 授予命名空间访问权

将命名空间 `team-a` 绑定到资源池 `training`：

```bash
hami-cli bind --namespace team-a --pool training
hami-cli binding list --namespace team-a
```

撤销绑定：

```bash
hami-cli unbind --namespace team-a --pool training
```

`unbind` 只撤销后续 Pod 使用该资源池的权限，不表示已有 Pod 会被删除或立即回收 GPU。

## Kubernetes 配置与权限

`hami-cli` 使用 Kubernetes `client-go` 的标准 kubeconfig 加载规则。常用全局参数包括：

- `--kubeconfig`：指定 kubeconfig 文件。
- `--context`：选择 kubeconfig context。
- `--namespace` / `-n`：覆盖当前 context 的默认命名空间。
- `--server`、`--token` 和客户端证书参数：直接覆盖连接配置。
- `--as`、`--as-group` 和 `--as-uid`：使用 Kubernetes impersonation。

使用 `hami-cli --help` 查看全部参数。

只读命令需要读取 `ResourcePool`、`GPUDevice` 或 `ResourcePoolBinding`。修改命令还需要对应对象的 `create`、`patch` 或 `delete` 权限。最终授权结果由集群 RBAC、准入策略和 HAMi Enterprise 服务端共同决定。

`ResourcePoolBinding` 是命名空间资源：

- 未显式指定命名空间时，使用当前 kubeconfig context 的默认命名空间。
- `binding list --all-namespaces` 不能与 `--namespace` 同时使用。
- `apply` 输入已包含命名空间且同时传入不同的 `--namespace` 时，命令拒绝操作。

## 从 YAML 或 JSON 应用对象

`apply` 接受 `ResourcePool`、`GPUDevice` 和 `ResourcePoolBinding`，支持多文档 YAML、JSON 文件和标准输入：

```yaml
apiVersion: resource.hami.io/v1alpha1
kind: ResourcePool
metadata:
  name: training
spec: {}
---
apiVersion: resource.hami.io/v1alpha1
kind: ResourcePoolBinding
metadata:
  name: training
  namespace: team-a
spec:
  poolRef:
    name: training
```

```bash
hami-cli apply -f resources.yaml
cat resources.yaml | hami-cli apply -f -
```

多文档输入按出现顺序逐个应用，不提供跨对象事务。后续文档失败时，之前已经成功应用的对象不会自动回滚。

`ResourcePool` 和 `ResourcePoolBinding` 使用字段管理器 `hami-cli` 执行 Server-Side Apply。字段所有权冲突会直接返回，不会强制接管其他管理器的字段。

`GPUDevice` 清单只能修改已由控制器发现的设备，不能创建新设备。修改前先导出最新对象，保留 `metadata.resourceVersion` 和 `spec.identity`，只调整 `spec.poolRef`：

```bash
kubectl get gpudevice GPUDEVICE_NAME -o yaml > device.yaml
# 编辑 device.yaml 中的 spec.poolRef
hami-cli apply -f device.yaml
```

如果 `resourceVersion` 已过期，错误信息会包含当前资源版本和资源池归属。设备身份与控制器清单不一致时，`apply` 也会停止，避免覆盖并发更新或伪造设备清单。

## kubectl 插件和命令补全

将同一二进制以 `kubectl-hami_cli` 的名称放入 `PATH` 后，可以使用 kubectl 插件形式执行：

```bash
kubectl hami-cli pool list
```

可为当前 shell 加载命令补全：

```bash
source <(hami-cli completion bash)
source <(hami-cli completion zsh)
```

Fish 和 PowerShell 也可通过 `hami-cli completion --help` 查看生成方式。

## 容器镜像

容器镜像支持 `linux/amd64` 和 `linux/arm64`。GHCR package 当前需要登录；GitHub 用户必须具有该 package 的读取权限，并使用包含 `read:packages` scope 的 personal access token (classic)。组织启用 SSO 时，还需要为 token 授权 SSO：

```bash
echo "$CR_PAT" | docker login ghcr.io -u GITHUB_USERNAME --password-stdin
docker run --rm ghcr.io/dynamia-ai/hami-cli:latest version
```

需要访问集群时，只读挂载 kubeconfig，并使用宿主机 UID/GID 运行：

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --volume "$HOME/.kube:/kube:ro" \
  --env KUBECONFIG=/kube/config \
  ghcr.io/dynamia-ai/hami-cli:latest pool list
```

容器使用时注意以下限制：

- kubeconfig 中的 API Server 必须能从容器网络访问。宿主机 `localhost` 不代表容器内的宿主机。
- 当前 distroless 镜像不包含 `aws`、`gcloud`、`kubelogin` 等外部程序，不能直接执行 kubeconfig 的 `exec` 认证插件。
- GHCR 标签可以被更新。稳定发布的 `image-digest.txt` 和 workflow run summary 会提供 `image@sha256:...` 不可变引用。

## 常见问题

### 服务器找不到资源类型

先确认 kubeconfig context 和 CRD：

```bash
kubectl config current-context
kubectl api-resources --api-group=resource.hami.io
```

目标集群没有列出 `ResourcePool`、`GPUDevice` 和 `ResourcePoolBinding` 时，先安装或修复 HAMi Enterprise 资源池组件。

### 请求返回 `forbidden`

使用同一身份检查具体操作权限：

```bash
kubectl auth can-i list resourcepools.resource.hami.io
kubectl auth can-i patch gpudevices.resource.hami.io
kubectl auth can-i create resourcepoolbindings.resource.hami.io -n team-a
```

不要通过 `--insecure-skip-tls-verify` 或更换 context 绕过 RBAC 问题。

### `GPUDevice` 更新发生冲突

重新导出最新对象，确认当前资源池归属，再应用修改。不要删除 `metadata.resourceVersion`，也不要修改 `spec.identity`。

### 容器内认证失败

检查 kubeconfig 是否依赖 `exec` 插件，以及 API Server 地址是否指向宿主机回环地址。依赖外部认证程序时，优先在宿主机运行二进制。

## 开发

开发环境需要 Go 1.26.0 或更高的 Go 1.26 补丁版本，并需要读取私有资源池 API 模块。使用组织认可的 GitHub 凭据配置私有模块访问后，执行：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off go build -o bin/hami-cli .
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off go test ./...
```

提交前运行仓库完整检查：

```bash
make ci
```

发布渠道、Actions 配置和不可变发布约束见 [发布维护说明](docs/releasing.md)。厂商运行时插件的目标边界见 [架构说明](docs/architecture.md)。
