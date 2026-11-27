# hami-cli 用户指南

`hami-cli` 是面向 HAMi Enterprise 集群管理员和平台运维人员的命令行工具，用于在 Kubernetes 集群中管理物理设备资源池、配额、默认调度策略和命名空间访问权限。

使用前，目标集群需要已经安装 HAMi Enterprise、Resource Pool Manager（RPM）及配套 CRD，本机也需要准备好访问目标集群的 `kubeconfig`。按日常运维需求，当前身份通常需要以下 RBAC 权限：

- 读写 `ResourcePool`、`ResourcePoolBinding` 和 `GPUDevice`（使用正则授权时还需读写 `ResourcePoolRegexBinding`）。
- 跨命名空间执行 `list` 以查询 `ResourcePoolBinding` 和 `GPUDevice`（`pool describe` 和 `pool bindings` 依赖这项权限）。
- 集群级 `list namespaces` 权限（仅在运行正则预览 `--preview` 时需要）。

## 安装并连接集群

拿到新集群的访问凭证后，先在本地终端或跳板机准备好 `hami-cli`，并确认它能连上目标集群。

### 一键安装

在 Linux 或 macOS（支持 `amd64` 和 `arm64`）终端运行以下命令，无需 `root` 权限，脚本也不会调用 `sudo`：

```bash
curl -fsSL https://storage.dynamia.ai/hami-cli/install.sh | sh
```

主下载地址不可达时，改用备用地址：

```bash
curl -fsSL https://public.hami.run/hami-cli/install.sh | sh
```

需要指定安装版本或安装目录时，将环境变量传给管道后的 `sh`（不要只写在 `curl` 之前）：

```bash
curl -fsSL https://storage.dynamia.ai/hami-cli/install.sh | \
  HAMI_CLI_VERSION=v0.1.0 HAMI_CLI_INSTALL_DIR="$HOME/bin" sh
```

安装脚本会校验归档的 SHA-256 摘要，然后依次完成三项配置：

- 将可执行文件安装到 `~/.local/bin/hami-cli`（默认路径）。
- 生成 Bash、Zsh 和 Fish 补全文件，并在默认安装路径下为当前默认 shell 启用补全，重新打开终端后生效。Bash 补全要求本机已安装 `bash-completion`。
- 调用已安装的二进制为本地 AI 编程助手安装内置的 `managing-hami-resource-pools` Skill。

如果新开的终端找不到 `hami-cli`，将 `~/.local/bin` 加入 `PATH`：

```bash
export PATH="$HOME/.local/bin:$PATH"
```

在 Warp 终端中，内置输入编辑器可能与 Zsh 补全不兼容。如果新建窗口后补全不可用，运行 `exec /bin/zsh -l` 进入 Zsh 登录会话。

安装脚本在装完二进制和补全文件后仍会执行 Skill 安装；如果非交互环境无法自动识别 AI 助手，这一步会失败并导致脚本以非零状态退出。设置 `HAMI_CLI_AGENT` 为目标助手名称后重新执行安装脚本即可，例如安装到 Codex：

```bash
curl -fsSL https://storage.dynamia.ai/hami-cli/install.sh | HAMI_CLI_AGENT=codex sh
```

### 从 GitHub Releases 手动下载

在 Windows 环境下，或在受控网络中不便直接执行远程安装脚本时，可以从公开的 [GitHub Releases](https://github.com/dynamia-ai/hami-cli/releases) 页面下载目标版本（例如 `v0.1.0`）的平台归档。每个版本均提供五个平台的 `.tar.gz` 归档、对应的逐文件 `.sha256`、汇总校验文件 `checksums.txt` 和容器镜像摘要 `image-digest.txt`：

| 操作系统 | 支持架构 | 归档内可执行文件 |
| --- | --- | --- |
| Linux | `amd64`、`arm64` | `hami-cli` |
| macOS | `amd64`、`arm64` | `hami-cli` |
| Windows | `amd64` | `hami-cli.exe` |

以 Linux `amd64` 的 `v0.1.0` 为例，下载归档和同名 `.sha256` 文件后，核对摘要并安装到 `~/.local/bin`（macOS 将 `sha256sum` 替换为 `shasum -a 256`）：

```bash
printf '%s  %s\n' "$(cat hami-cli_v0.1.0_linux_amd64.tar.gz.sha256)" \
  hami-cli_v0.1.0_linux_amd64.tar.gz | sha256sum -c -
tar -xzf hami-cli_v0.1.0_linux_amd64.tar.gz
install -m 0755 hami-cli_v0.1.0_linux_amd64/hami-cli ~/.local/bin/hami-cli
```

如果下载了全部平台归档和 `checksums.txt`，可以在同一目录运行 `sha256sum -c checksums.txt` 批量校验。Windows 用户解压 `hami-cli_v0.1.0_windows_amd64.tar.gz` 后，将目录内的 `hami-cli.exe` 放入 `PATH` 目录即可。

手动安装不会自动配置 shell 补全和内置 Skill。如需启用，安装后通过 `hami-cli completion --help` 查看补全生成命令，并参考后文安装内置 Skill。

### 作为 kubectl 插件或容器镜像运行

团队习惯统一通过 `kubectl` 操作集群时，可以将 `hami-cli` 二进制复制或软链接为 `kubectl-hami_cli` 并放入 `PATH` 目录，之后直接执行 `kubectl hami-cli`。

在 CI 流水线或不方便安装本地二进制的跳板机上，可以直接运行多架构容器镜像 `ghcr.io/dynamia-ai/hami-cli`（支持 `linux/amd64` 和 `linux/arm64`，镜像入口点已设为 `hami-cli`）。将本地 `kubeconfig` 以只读方式挂载进容器，替换 `<IMAGE_TAG>` 为目标版本标签（需要固定不可变镜像时，使用对应 Release 中 `image-digest.txt` 提供的摘要，以 `ghcr.io/dynamia-ai/hami-cli@<digest>` 引用）并显式指定 `--context`：

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,src="/absolute/path/to/kubeconfig",dst=/kubeconfig,readonly \
  ghcr.io/dynamia-ai/hami-cli:<IMAGE_TAG> \
  --kubeconfig /kubeconfig \
  --context TARGET_CONTEXT \
  --request-timeout 20s \
  pool list
```

如果 `kubeconfig` 中的集群地址是 `127.0.0.1` 或 `localhost`，容器内会指向容器自身。在 macOS 的 Docker Desktop 或 OrbStack 上，用宿主机网关地址和实际 API 端口覆盖 `--server`，并传入当前集群配置里的 `tls-server-name` 或服务端证书 SAN 对应的 `--tls-server-name`，不要使用 `--insecure-skip-tls-verify`：

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,src="/absolute/path/to/kubeconfig",dst=/kubeconfig,readonly \
  ghcr.io/dynamia-ai/hami-cli:<IMAGE_TAG> \
  --kubeconfig /kubeconfig \
  --context TARGET_CONTEXT \
  --request-timeout 20s \
  --server "https://host.docker.internal:<API_PORT>" \
  --tls-server-name NAME_FROM_KUBECONFIG_OR_CERTIFICATE \
  pool list
```

在 Linux Docker 上，如果 API Server 仅监听宿主机回环地址 `127.0.0.1`，`--add-host host.docker.internal:host-gateway` 指向宿主机网桥而非回环地址，无法连通该端口；此时改用 `--network host` 配合 `kubeconfig` 中的原回环地址（或通过端口转发访问），并保持 TLS 校验开启。只有在 API Server 监听宿主机非回环网卡时，才适用 `--add-host host.docker.internal:host-gateway`。

如果 `kubeconfig` 引用了外部 CA、客户端证书或私钥文件，将这些文件只读挂载到容器内与 `kubeconfig` 引用路径一致的位置（或同步修改挂载后 `kubeconfig` 中的文件路径）。`hami-cli` 容器镜像基于 distroless，无法执行 `kubeconfig` 中的外部 `exec` 认证插件，依赖这类插件的集群改用宿主机二进制。

### 验证安装并指定目标集群

先确认本地版本信息：

```bash
hami-cli version
```

命令会输出客户端版本号、源码提交、资源池 API 版本和运行平台。后续在多集群环境中操作时，建议在每条命令上显式传入 `--context` 和 `--request-timeout`，避免误写到默认上下文或因网络异常长时间挂起：

```bash
hami-cli --context prod-gpu --request-timeout 20s pool list
```

`hami-cli` 支持标准的 Kubernetes 连接参数，包括 `--kubeconfig`、`--context`、`--namespace`（简写 `-n`）、`--request-timeout`、`--server`、`--as` 和 `--as-group`。`--request-timeout` 只控制单次 API 请求超时，不会截短后文 `pool update` 和 `pool delete` 的等待上限（`--wait-timeout`）。下文示例为便于阅读省略了 `--context` 和 `--request-timeout`，实际执行时按集群情况补上即可。

## 为训练团队划分第一个专属 GPU 资源池

假设视觉训练团队的业务运行在命名空间 `cv-train` 下，此前与在线推理共享节点时经常出现争抢。现在需要把一批空闲的 NVIDIA 物理 GPU 划成名为 `training` 的独立资源池，只授权给 `cv-train` 使用。

### 盘点可用设备

把设备划入资源池之前，先查看集群里已被 RPM 发现的物理设备清单：

```bash
hami-cli device list
```

输出按节点、设备类型和设备 ID 排序，包含 `NAME`、`NODE`、`DEVICE-TYPE`、`DEVICE-ID`、`POOL`、`DISCOVERED` 和 `HEALTHY` 七列。只想查看某一台宿主机上的设备时，加上 `--node`：

```bash
hami-cli device list --node gpu-node-01
```

在动任何设备之前（包括 `POOL` 显示为 `<unassigned>` 的未入池设备），先用 `hami-cli device list -o json` 检查设备的 `status.allocations`，并与相关团队确认或协调冻结调度。未入池的设备仍可能承载任务，分配列表为空时也可能有并发调度正在进行。确认目标设备空闲，且 `DISCOVERED` 与 `HEALTHY` 均为 `true` 后，再将其划入新池。

### 创建资源池并划入设备

先创建 `training` 资源池（名称需符合 Kubernetes DNS 标签规范，长度不超过 63 个字符，只包含小写字母、数字和连字符）：

```bash
hami-cli pool create training
```

创建成功后，终端输出 `resourcepool.resource.hami.io/training created`。通过 `pool list` 或 `pool describe training` 可以看到，刚建好的空池 `DISCOVERED` 和 `HEALTHY` 均为 `0`；在划入健康设备并完成命名空间授权之前，这个池还无法承载业务任务。

接着把选中的空闲 GPU 划入 `training`。可以直接传入 `device list` 中的设备对象名，也可以传入节点名和物理设备 ID（两种方式不能混用）：

```bash
hami-cli device assign gpu-0123456789abcdef0123456789abcdef --pool training
```

或者通过节点与设备 ID 指定（省略 `--device-type` 时默认为 `NVIDIA`）：

```bash
hami-cli device assign \
  --node gpu-node-01 \
  --device-id GPU-11111111-2222-3333-4444-555555555555 \
  --pool training
```

命令成功时会打印 `gpudevice.resource.hami.io/<设备对象名> assigned to training`。一台物理设备同一时间只能归属一个资源池；对已经归属其他池的设备再次执行 `device assign` 会直接把设备迁移到新池，不会报错。写入时如果设备对象已被其他操作修改，请求会因冲突失败，重新查看设备状态后再决定是否重试。

### 授权业务命名空间并核对结果

设备到位后，授予 `cv-train` 命名空间使用 `training` 资源池的权限：

```bash
hami-cli pool bind training --namespace cv-train
```

命令会在 `cv-train` 命名空间内创建同名的 `ResourcePoolBinding` 对象。如果同名绑定已经存在，`pool bind` 会报 `AlreadyExists` 而不更新已有对象；需要修改已有绑定时改用 `hami-cli apply -f`。

`pool bind` 返回成功只表示绑定对象已创建，此时控制器可能尚未完成校验（`BOUND` 可能显示为 `Unknown`）。随后用 `pool describe` 在一个视图里核对交付状态：

```bash
hami-cli pool describe training
```

输出会依次列出资源池的配额、调度策略、已发现和健康设备数、`Explicit namespace bindings:`、`Regex bindings:`（存在匹配规则时显示）以及 `Devices:` 设备表。确认以下三项都已满足：

- `Discovered devices` 和 `Healthy devices` 数量与划入的设备一致。
- `Explicit namespace bindings:` 下出现 `cv-train`，且 `BOUND` 变为 `True`。
- `Devices:` 下列出目标物理设备，且 `DISCOVERED` 与 `HEALTHY` 均为 `true`。

这些都确认后，训练团队在 `cv-train` 命名空间提交申请 GPU 的 Pod，RPM 就会把任务限定在 `training` 池的物理卡上。如果一个命名空间同时获准使用多个资源池，业务方可以在 Pod 上添加注解 `hami.io/resource-pool: training` 显式指定目标池。

## 随业务扩容批量授权和回收命名空间

几个星期后，训练部门在原有 `cv-train` 之外又按课题拆分出 `cv-train-det`、`cv-train-seg`、`nlp-train-sft` 等多个命名空间，而且每周还会新建实验命名空间。逐个手动执行 `pool bind` 维护成本高，也容易遗漏。

面对按规范命名的批量命名空间，可以用一条集群级正则规则覆盖现有和未来所有匹配的命名空间。正则语法采用 Go RE2，长度限制为 1 至 256 字节，始终按完整命名空间名称匹配，不会因为局部子串命中而误授权。

### 预览正则匹配范围

预览模式仅需要集群级 `list namespaces` 权限，不依赖正则 CRD，也不会检查目标资源池是否存在或表达式是否重复。即使集群尚未升级支持正则绑定，也可以先用 `--preview` 验证表达式。为防止 shell 展开通配符，用单引号包住正则表达式：

```bash
hami-cli pool regex-bindings create training '.*-train(-.*)?' --preview
```

终端会输出当前匹配总数和按字母排序的命名空间名称，例如：

```text
Pool: training
Matching namespaces: 4
cv-train
cv-train-det
cv-train-seg
nlp-train-sft
```

从预览输出可以看到，原有的 `cv-train` 以及新建的三个训练命名空间都在命中范围内。解读预览结果时注意两点：

- 统计数量是执行时刻的快照，包含已经通过显式绑定或其他规则获准的命名空间。
- 即使当前匹配数为 `0`，正式创建规则后，未来新建且符合该表达式的命名空间依然会自动获得访问权。

### 创建和调整正则规则

正式创建、查看、更新或删除正则规则要求集群已安装 `ResourcePoolRegexBinding` CRD，且 RPM 版本支持正则绑定。确认预览范围符合预期后，去掉 `--preview` 提交规则：

```bash
hami-cli pool regex-bindings create training '.*-train(-.*)?'
```

命令成功后输出形如 `resourcepoolregexbinding.resource.hami.io/rprb-9f8e7d6c5b4a32109f8e7d6c5b4a3210 created` 的结果。创建时不需要自定规则名，`hami-cli` 会自动生成规则 ID。同一个资源池下不能重复创建完全相同的表达式，不同资源池可以各自使用相同的表达式。

查看单个资源池或全集群的正则规则：

```bash
hami-cli pool regex-bindings list --pool training
hami-cli pool regex-bindings list
```

运行 `hami-cli pool describe training`，可在 `Regex bindings` 表的 `MATCHING NAMESPACES` 列查看每条规则当前命中的命名空间。结果按名称排序，涵盖已通过其他绑定获准的命名空间。没有命中时显示 `<none>`。预览需要集群级 `list namespaces` 权限。读取列表失败时，CLI 向标准错误输出写入警告，预览列显示 `<unavailable>`，并继续输出资源池详情。

如果后续命名规范收紧，改为只匹配视觉团队的训练命名空间（保留 `cv-train`、`cv-train-det` 和 `cv-train-seg`，不再包含 `nlp-train-sft`），可以按规则 ID 原地更新表达式。更新和删除都既支持裸 ID，也支持 `list -o name` 输出的完整资源名：

```bash
hami-cli pool regex-bindings update rprb-9f8e7d6c5b4a32109f8e7d6c5b4a3210 'cv-train(-.*)?'
```

更新规则会保留原有的规则 ID 和所属资源池。如果需要把一条正则授权改指向另一个资源池，按业务切换顺序先在目标池创建规则，再删除旧池规则。

### 收回权限前的核对

撤销单个显式绑定时，运行 `pool unbind`（该命令只按资源池名和命名空间删除，不核对 UID；在多人协作环境中删除前先复核目标绑定，避免误删重建后的同名绑定）：

```bash
hami-cli pool unbind training --namespace cv-train
```

删除正则规则时，传入规则 ID 或完整资源名：

```bash
hami-cli pool regex-bindings delete rprb-9f8e7d6c5b4a32109f8e7d6c5b4a3210
```

只有同时满足下面两条，才能确认目标命名空间已经失去访问权：

- 已经没有指向目标池的显式绑定，也没有能匹配该命名空间的正则规则。两类授权互不替代，命名空间命中任意一类即拥有访问权，核对时两类列表都要检查，或者直接通过 `hami-cli pool describe training` 查看 `Explicit namespace bindings:` 和 `Regex bindings:`：

```bash
hami-cli pool bindings training
hami-cli pool bindings -A
hami-cli pool regex-bindings list --pool training
```

- 已有旧任务已结束。撤销显式绑定或删除正则规则都不会驱逐正在运行的 Pod，只对后续新创建的 Pod 生效。

`pool bindings` 必须指定单个池名或 `-A`（`--all-pools`），两者不能同时使用，也不支持 `--namespace` 参数。

## 用配额和默认调度策略治理共享资源池

多项目共用 `training` 资源池后，运维团队很快遇到两类问题：个别作业占满了节点上的 CPU 和宿主内存，导致同池其他任务无法启动；部分训练任务集中在同一台节点造成热点，而单机多卡训练又没有优先落到互联拓扑更优的卡上，逐个要求业务方修改 Pod 调度注解难以落地。这时可以在资源池级别统一配置默认调度策略和 CPU、内存配额。

### 配置默认调度策略

为 `training` 资源池配置节点级和卡级默认调度策略：节点层用 `spread` 把任务分散到不同宿主机以缓解单机热点，卡级用 `topology-aware,mutex` 优先按机内互联拓扑选卡并按互斥规则隔离任务：

```bash
hami-cli pool update training \
  --node-scheduler-policy spread \
  --gpu-scheduler-policy topology-aware,mutex
```

两个参数的合法取值和适用场景如下：

| 参数 | 可选值 | 说明 |
| --- | --- | --- |
| `--node-scheduler-policy` | `binpack`、`spread` | 单选。`binpack` 优先将任务集中到已有负载的节点以提高整机利用率；`spread` 优先将任务分散到负载较低的节点以降低单机争抢。 |
| `--gpu-scheduler-policy` | `binpack`、`spread`、`topology-aware`、`mutex`、`numa` | 支持逗号分隔的有序组合。`binpack` 与 `spread` 控制节点内单卡集中或分散；`topology-aware` 优先按机内互联拓扑选卡；`mutex` 按互斥规则避免冲突任务同卡混部；`numa` 优先按 NUMA 亲和性选卡。 |

资源池的默认调度策略会在新 Pod 准入时注入 `hami.io/node-scheduler-policy` 和 `hami.io/gpu-scheduler-policy`。如果业务方已经在 Pod 上显式声明了对应注解，以 Pod 自身配置为准。

### 设置和取消 CPU 与内存配额

配置配额前，先核对三条运行规则：

- 配额按容器的 `limits` 汇总核算，与 `requests` 无关。启用了某个维度后，进入该池的工作负载必须为该维度声明正数 `limit`（Pod 级或各容器级，按集群准入规则校验），否则会被准入拒绝。
- 未设置的维度默认不限制。数值 `0` 只限制被设为 `0` 的那一个维度，会阻止该维度的所有非零资源申请，不影响未设置的另一个维度。
- 取消某个维度的上限时，在 `pool update` 中将该维度设为 `unlimited`（`pool create` 不接受 `unlimited`）。把两个维度都设为 `unlimited`，或取消最后一个已有限额的维度后，资源池的配额限制会被清空。

可以在创建池时通过 `pool create` 设置配额，也可以随时用 `pool update` 调整配额。`pool update` 只修改显式传入的参数，未传入的策略和配额维度保持原样：

```bash
hami-cli pool update training \
  --cpu-quota 128 \
  --memory-quota 512Gi \
  --wait-for-current
```

取消 CPU 配额但保留内存配额：

```bash
hami-cli pool update training \
  --cpu-quota unlimited \
  --wait-for-current
```

如果在开启配额的瞬间有 Pod 正在准入流程中，服务端可能会拒绝本次开启请求。等待在途准入完成后重新读取资源池状态再重试即可。

### 确认配额账本已同步

配额规则写入 `spec.quota` 后，由 RPM 控制器异步核算已用和预留量并写回 `status.quota`。在 `pool update` 中加上 `--wait-for-current`，命令会在修改成功后轮询等待账本同步到本次写入的版本；默认总等待上限为 `30s`，可通过 `--wait-timeout` 调整（例如 `--wait-timeout 1m`，必须配合 `--wait-for-current` 使用）。`--wait-timeout` 与全局 `--request-timeout` 相互独立。

执行 `hami-cli pool describe training` 查看配额状态时，先看 `Quota ledger` 这一行（未配置配额时不会显示这一行）：

- 显示 `current (generation N)` 时，`CPU used`、`CPU reserved`、`Memory used` 和 `Memory reserved` 已经按这一版配置核算完成，可以结合 `CPU quota`、`Memory quota` 判断剩余容量（这四项是用量与预留，不是配额上限）。
- 显示 `pending (current generation N)` 或 `stale (observed generation X, current generation Y)` 时，控制器尚未同步完成，稍后重新读取。

如果 `--wait-for-current` 等待超时，或等待期间被其他并发操作覆盖而中断退出，此前提交的配额配置本身已经写入集群，不会被回滚。先执行 `pool describe` 确认当前配置和账本状态，再决定下一步动作。

## 接入昇腾 Ascend 设备

当集群扩入华为昇腾（例如 `Ascend310P` 或 `Ascend910B`）节点时，沿用同一套资源池流程即可纳管。当前资源池支持范围为 NVIDIA 和已启用的 Ascend 设备类型。

### 确认集群前提并查看设备标识

`hami-cli` 的设备参数只用来选择集群已发现的对象，不会自动开启服务端能力。划分 Ascend 资源池前，确认集群已完成以下准备：

- 集群已安装 `0.4.0` 版资源池 CRD Chart。
- RPM 已启用 `resourcePoolManager.enabled`、`devices.ascend.enabled` 以及配套的 `vnpus.configs` 配置。

查看目标节点上已被发现的设备清单：

```bash
hami-cli device list --node npu-node-01
```

需要同时查看完整身份信息、容量和当前分配记录时，以 JSON 格式读取：

```bash
hami-cli device list --node npu-node-01 -o json
```

### 划入或移出昇腾设备

为推理业务创建 `npu-infer` 资源池并将 Ascend 设备划入时，传入 `spec.identity.deviceType` 和 `spec.identity.deviceID` 中的精确值：

```bash
hami-cli pool create npu-infer
hami-cli device assign \
  --node npu-node-01 \
  --device-type Ascend310P \
  --device-id 01-aBc_0 \
  --pool npu-infer
```

也可以直接使用 `device list` 返回的 `GPUDevice` 对象名（`hami-cli device assign <GPUDevice对象名> --pool npu-infer`），对象名不能与 `--node`、`--device-type`、`--device-id` 混用。按物理身份操作时注意以下两点：

- `--device-type` 区分大小写，必须与 `spec.identity.deviceType` 一致（例如 `Ascend310P`、`Ascend910B`），省略时默认为 `NVIDIA`。
- `--device-id` 原样复制集群上报的 `spec.identity.deviceID`，保留大小写、前导零和分隔符，不要求 UUID 格式。

迁移或移出任何设备前，先通过 `device list -o json` 检查 `status.allocations` 并确认负载。解读昇腾设备状态时注意两点：`status.vgpuCount` 表示共享槽位数，而非物理卡数量（每个 `GPUDevice` 对象代表一张物理设备）；在昇腾硬切分或未知模式下，设备清单不返回 `status.corePercent`，硬切分分配记录中的 `corePercent` 也可能为 `0`，因此不能把 `corePercent` 缺失或为 `0` 当成设备空闲。

把设备从资源池移出回空闲状态时，使用 `device unassign`（同样支持直接传 `GPUDevice` 对象名，或传 `--node`、`--device-type`、`--device-id`，两种方式不能混用）。在多人同时运维的集群里，建议从执行前的 `device list -o json` 读取该设备的 `metadata.uid`，并同时传入 `--expected-uid` 和 `--expected-pool`（两个参数必须成对提供且不能为空）：

```bash
hami-cli device unassign \
  --node npu-node-01 \
  --device-type Ascend310P \
  --device-id 01-aBc_0 \
  --expected-uid DEVICE_UID \
  --expected-pool npu-infer
```

如果执行瞬间设备的 UID 或所属池已经变化，`hami-cli` 会在写入前直接中止，防止误清他人刚重新分配的设备。

## 接入声明式配置与 AI 助手工作流

当资源池数量增多后，平台团队通常希望把配置纳入 Git 评审与 CI 流水线，或者把日常巡检和变更交给 AI 助手辅助执行。

### 以结构化格式导出数据

`pool list`、`device list`、`pool bindings` 和 `pool regex-bindings list` 都支持 `-o`（`--output`）参数，可选 `table`（默认）、`name`、`json` 和 `yaml`。例如结合 `jq` 筛选指定命名空间在 `training` 池下的显式绑定：

```bash
hami-cli pool bindings training -o json | \
  jq --arg ns "cv-train" '.items[] | select(.metadata.namespace == $ns)'
```

### 用配置文件声明资源池状态

使用 `hami-cli apply -f` 可以直接提交 YAML 或 JSON 清单（传入 `-f -` 从标准输入读取）。例如将资源池和显式绑定写在同一份 `training-pool.yaml` 中：

```yaml
apiVersion: resource.hami.io/v1alpha1
kind: ResourcePool
metadata:
  name: training
spec:
  quota:
    cpu: "128"
    memory: 512Gi
  workloadDefaults:
    schedulingPolicy:
      node: spread
      gpu: topology-aware,mutex
---
apiVersion: resource.hami.io/v1alpha1
kind: ResourcePoolBinding
metadata:
  name: training
  namespace: cv-train
spec:
  poolRef:
    name: training
```

提交清单：

```bash
hami-cli apply -f training-pool.yaml
```

`apply -f` 仅接受 `apiVersion: resource.hami.io/v1alpha1`，不同资源类型的支持范围和规则如下：

| 资源类型 | 是否支持 `apply` | 关键规则 |
| --- | --- | --- |
| `ResourcePool` | 支持 | 通过 `apply` 写入的字段归 `hami-cli` 管理，其他工具维护的字段会保留。 |
| `ResourcePoolBinding` | 支持 | 严格校验字段；`metadata.name` 必须与 `spec.poolRef.name` 相同；CLI 自动补齐 `resource.hami.io/pool` 标签。清单省略 `metadata.namespace` 时，用命令行 `--namespace`；未传该参数时用当前上下文。清单里的命名空间与命令行显式传入的 `--namespace` 不一致时，`apply` 拒绝执行。 |
| `GPUDevice` | 仅支持更新已有对象 | 不能用来新建设备，也不接受 `device list -o json` 输出的列表对象；需提取单个设备对象并补上 `apiVersion` 和 `kind: GPUDevice`。清单必须包含最新的 `metadata.resourceVersion` 和精确的 `spec.identity`；省略 `spec.poolRef` 表示移出资源池。 |
| `ResourcePoolRegexBinding` | 不支持 | 会报 `unsupported kind`，改用 `hami-cli pool regex-bindings` 子命令管理。 |

多文档清单按文件顺序逐个提交，每成功一个输出一行确认信息；遇到首个错误时停止，此前已经成功的文档会保留在集群中，排查状态后再决定是否继续。如果出现字段归属冲突，说明其他控制器或工具正管理同一字段，先确认归属关系，不要强行接管。

### 为 AI 助手安装内置 Skill

`hami-cli` 内置了名为 `managing-hami-resource-pools` 的 Agent Skill，包含基线采集、变更核验和安全清理规范。通过一键脚本安装时，如果本机有可识别的 AI 助手，脚本已经自动完成了安装。

需要在容器环境中安装、切换其他助手、检查状态或卸载时，直接使用 `skill` 子命令：

```bash
hami-cli skill status --scope user --agent codex
hami-cli skill install --scope user --agent codex --yes
hami-cli skill uninstall --scope user --agent codex --yes
```

在容器镜像中为宿主机安装 Skill 时，无需挂载 `kubeconfig`，以宿主机 UID 和 GID 运行并可写挂载对应目录（例如 `$HOME/.agents`）：

```bash
mkdir -p "$HOME/.agents"
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/hami-home \
  --mount type=bind,src="$HOME/.agents",dst=/hami-home/.agents \
  ghcr.io/dynamia-ai/hami-cli:<IMAGE_TAG> \
  skill install --scope user --agent codex --yes
```

## 安全下线不再使用的资源池

季度专项结束，需要撤销临时训练池 `training-temp` 并释放其中的物理 GPU。删除资源池前，必须先处理指向该池的绑定和设备引用；如果仍有显式绑定、正则规则或设备引用，直接执行 `pool delete` 可能被准入拒绝。

### 按顺序解除引用并删池

在共享集群中，推荐按与建池相反的顺序手动逐项核对并清理：

1. 运行 `hami-cli pool describe training-temp`，列出当前所有显式绑定、正则规则和物理设备。
2. 依次撤销显式绑定（`hami-cli pool unbind training-temp --namespace <ns>`）并删除正则规则（`hami-cli pool regex-bindings delete <rule-id>`）。
3. 确认池内设备不再承载活跃业务后，用 `hami-cli device unassign <device> --expected-uid <uid> --expected-pool training-temp` 将设备释放回未分配状态。
4. 确认所有引用已清空，执行删池命令：

```bash
hami-cli pool delete training-temp
```

默认情况下（`--wait=true`），集群受理删除请求后终端会先打印 `resourcepool.resource.hami.io/training-temp deletion requested`，然后最多等待 2 分钟，确认原池（按原 UID 判断）已经消失后打印 `resourcepool.resource.hami.io/training-temp deleted`。如果在等待期间已有同名新池创建，命令仍会打印 `deleted`；如果后续流程要求池名本身为空，再通过 `hami-cli pool list` 确认。

如果集群负载较高、需要更长等待时间，可以通过 `--wait-timeout 5m` 放宽上限；如果希望提交请求后立刻返回，传入 `--wait=false`（此时不能同时指定 `--wait-timeout`）。

### 确认整池拆除时使用强制清理

执行前确认：指向该池的显式绑定和正则规则会被删除，池内设备会马上变为未分配，可能被重新划入其他资源池；运行中的 Pod 不会被驱逐，仍可能继续占用这些物理设备。

确认该资源池及其所有关联引用都需要一并拆除时，使用 `--force`：

```bash
hami-cli pool delete training-temp --force
```

`--force` 会先记下目标资源池的 UID，在每轮清理前重新核对（发现 UID 不一致立即停止），然后循环删除指向该池的显式绑定与正则规则、解除指向该池的设备归属（解绑设备时保留无关标签），最多进行 5 轮快照校验；确认没有残留引用后删除资源池并等待删除完成。整个过程不会影响其他资源池的绑定和设备。

如果在某一轮清理中发生错误，或 5 轮后仍有持续新增的引用，命令会停止且不发起删池请求。之前已经执行的清理不会回滚，先重新核对绑定、正则规则和设备列表后再决定是否重试。

### 处理删除超时与同名重建受阻

如果 `pool delete` 等待超时，终端会报错且不打印 `deleted`，但集群仍在后台继续处理这次删除请求。常见原因是资源池上存在 `resource.hami.io/reference-protection` 等 finalizer，正在等待关联引用释放。运行 `hami-cli pool list -o yaml` 查看对象是否带有 `metadata.deletionTimestamp`，并结合 `pool describe` 排查尚未清理的引用，不要手动摘除 finalizer。

如果刚删除的池名还有未终止的 Pod 持有旧池引用，API Server 会拒绝立刻创建同名的新资源池。等待相关 Pod 结束，或改用新的资源池名称即可；不要手工修改或删除 Pod 上由 RPM 维护的资源池归属信息。

## 常见报错处理

| 报错现象 | 原因与处理方式 |
| --- | --- |
| `pool regex-bindings create --preview` 报 `forbidden` | 当前身份缺少集群级 `list namespaces` 权限，为当前身份补充该权限后再预览。 |
| 其他读写命令报 `forbidden` | 当前身份的 RBAC 权限不足，或被集群准入策略拒绝。核对报错中的动作、资源和命名空间，按权限规范申请调整，不要绕过策略。 |
| 正则规则报重复、语法错误或字节数超限 | 同一资源池内已有完全相同的表达式，或表达式为空、超过 256 字节、不符合 Go RE2 语法。先用 `pool regex-bindings list --pool <pool>` 检查已有规则并修正表达式。 |
| 报 `NotFound` 或找不到 `ResourcePoolRegexBinding` | 目标集群未安装对应 CRD，或 RPM 版本尚未支持该资源。`hami-cli` 不会安装 CRD，需先在集群侧升级资源池 CRD 与 RPM。 |
| `apply -f` 报 `unsupported kind` | `apply` 仅支持 `ResourcePool`、`ResourcePoolBinding` 和已有 `GPUDevice`。正则规则改用 `pool regex-bindings` 子命令。 |
| `apply -f` 更新 `GPUDevice` 报版本缺失、版本过期或身份不匹配 | `GPUDevice` 由控制器维护，先重新导出最新对象，确认 `metadata.resourceVersion` 和 `spec.identity` 未被修改后再提交。 |
| `pool update`、`device assign`、`device unassign`、`pool regex-bindings update`、`pool regex-bindings delete` 或 `pool delete --force` 报写入冲突 | 对象在读取与写入之间被其他操作修改，重新查看当前状态后再决定是否重试。 |
| `pool create` 因同名未终止 Pod 被拒绝 | 集群中仍有非终态 Pod 引用同名旧池，等待这些 Pod 终止或更换资源池名称，不要手工改动 Pod 的资源池归属信息。 |
| 开启配额时因在途准入被拒绝 | 目标池有 Pod 正在准入过程中，等待准入结束后重新读取资源池状态并重试。 |
| 写操作发生超时或连接中断 | 无法直接确认变更是否已写入集群，按对象名、命名空间或 UID 重新读取状态后再决定重试或清理。 |
