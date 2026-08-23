# hami-cli

## 一键安装

Linux 和 macOS：

```bash
curl -fsSL https://storage.dynamia.ai/hami-cli/install.sh | sh
```

备用地址：

```bash
curl -fsSL https://public.hami.run/hami-cli/install.sh | sh
```

安装脚本会安装：

- `~/.local/bin/hami-cli`
- Bash、Zsh 和 Fish 补全文件
- hami-cli 内置的 Agent Skill

安装过程不需要 `root` 权限，也不会调用 `sudo`。
安装脚本会为当前默认 shell 启用补全；重新打开 shell 后生效。Bash 需要已经安装 `bash-completion`。

Warp 的内置输入编辑器可能与 Zsh 补全不兼容。如果新建 Warp 窗口后补全不可用，运行 `exec /bin/zsh -l` 进入 Zsh login shell。

如果新 shell 找不到 `hami-cli`，将 `~/.local/bin` 加入 `PATH`：

```bash
export PATH="$HOME/.local/bin:$PATH"
```

非交互环境无法自动识别 Agent 时，显式指定 Agent。例如安装到 Codex：

```bash
curl -fsSL https://storage.dynamia.ai/hami-cli/install.sh | HAMI_CLI_AGENT=codex sh
```

## Ascend 设备资源池

Ascend 资源池需要服务端具备对应支持，并安装 `0.4.0` 版 CRD Chart，启用
`resourcePoolManager.enabled`、`devices.ascend.enabled` 和对应的 `vnpus.configs`
配置。CLI 参数不会启用服务端能力；当前资源池支持范围为 NVIDIA 和已启用的 Ascend 类型。

先查看设备身份和当前分配，再使用实际值操作：

```bash
hami-cli --context TARGET_CONTEXT --request-timeout 20s device list -o json
hami-cli --context TARGET_CONTEXT --request-timeout 20s device assign \
  --node NODE_NAME --device-type Ascend310P --device-id DEVICE_ID --pool POOL_NAME
```

`--device-type` 使用 `spec.identity.deviceType` 中区分大小写的值，例如 `Ascend310P`
或 `Ascend910B`；省略时默认为 `NVIDIA`。`--device-id` 原样使用
`spec.identity.deviceID`，不要求 UUID 格式。`device unassign` 支持相同的身份参数。
也可以直接使用 `GPUDevice` 对象名，此时不能同时提供上述身份参数。

`device list` 和 `pool describe` 的表格包含 `DEVICE-TYPE` 列。操作前的分配检查、
临时解绑保护和服务端前提见[资源池操作指南](skills/managing-hami-resource-pools/references/operations.md#manage-pool-and-device-ownership)。

## NVIDIA Docker PoC

项目只发布一个用户侧 `hami-cli` 二进制。当前内置 NVIDIA 插件，不加载第三方动态插件，也尚未实现 Ascend 插件。

运行前需要满足以下条件：

- 操作系统和 CPU 架构为 Linux/AMD64；
- 已安装 NVIDIA 驱动、`nvidia-smi`、rootful Docker 和 NVIDIA Container Toolkit，当前 Docker endpoint 使用本机 `unix://` socket；
- 已准备 Linux/AMD64 的 `libvgpu.so`，并从可信发布清单或交付渠道取得制品版本和 SHA-256 摘要；
- 目标镜像为 Linux/AMD64 并使用 glibc；Core 文件权限允许当前 CLI 用户读取；
- `TMPDIR` 位于允许执行映射的本地文件系统；如果文件系统使用 `noexec` 挂载选项，CLI 会在启动 Docker 前停止；
- 仅选择一个物理 GPU 索引或 `GPU-...` UUID，不使用 MIG。

命令合同如下：

```text
hami-cli run --vendor nvidia [HAMi 参数] -- docker run [Docker 参数] IMAGE [COMMAND] [ARG...]
```

例如：

```bash
CORE=/opt/hami-core/libvgpu.so
CORE_VERSION=VERSION
CORE_SHA256=SHA256_FROM_TRUSTED_RELEASE_MANIFEST

hami-cli run \
  --vendor nvidia \
  --device 0 \
  --memory 8GiB \
  --compute 50 \
  --core "$CORE" \
  --core-version "$CORE_VERSION" \
  --core-sha256 "$CORE_SHA256" \
  -- \
  docker run --rm -it \
    --name training \
    -v "$PWD:/workspace" \
    -w /workspace \
    --network host \
    pytorch/pytorch:latest \
    python train.py --epochs 10
```

`CORE_SHA256` 不能从待验证文件现场计算后直接作为期望值。应从可信发布清单或交付渠道取得摘要，再由 CLI 对本地文件进行校验。

`--dry-run` 只生成经过 POSIX shell quoting 的最终命令，不检查 Docker endpoint 或本机 GPU。CLI 仍会校验 Core 文件的 SHA-256、ELF 架构、动态加载元数据和可加载共享库结构。dry-run 本身不会创建文件快照或修改 SELinux label。输出命令引用调用时校验的原始 Core 路径，并包含 `:ro,z`。执行该输出时，Docker 会对原始 Core 应用可供多个容器共享的 SELinux relabel，避免并发执行引用同一 Core 的 dry-run 命令时互相替换私有 MCS label；该命令不具备真实执行路径的字节快照固定保证：

```bash
hami-cli run \
  --vendor nvidia \
  --device 0 \
  --memory 8GiB \
  --compute 50 \
  --core "$CORE" \
  --core-version "$CORE_VERSION" \
  --core-sha256 "$CORE_SHA256" \
  --dry-run \
  -- docker run --rm ubuntu:22.04 nvidia-smi
```

真实执行时，CLI 把 Core、`--env-file` 和 `--label-file` 的字节复制到权限为 `0700` 的私有临时目录，再校验并使用只读快照。CLI 要求临时目录所在文件系统允许执行映射。Core 快照权限为 `0444`，Docker 使用私有 `Z` label 挂载该快照，因此使用 `--user` 的非 root SELinux 容器也能加载。即使原始路径随后被替换，Docker 读取的仍是通过校验的快照。

当前 PoC 只接受前台、一次性的 `--rm` 容器，并拒绝 `--detach` 和非 `no` 的 restart policy。清理辅助进程在前台 Docker 客户端退出后删除快照目录；`--rm` 保证容器不会在快照删除后被再次启动。

CLI 在 `docker run` 后插入 `--platform linux/amd64`、`--gpus`、`no-new-privileges=true`、Core 只读挂载、私有 tmpfs 状态目录、HAMi Core 环境变量、最低 NVIDIA driver capabilities 和可审计标签。固定平台参数让 Docker 从多架构镜像中解析 Linux/AMD64 变体；调用方显式指定其他平台时，CLI 会在启动 Docker 前拒绝。CLI 显式设置 `CUDA_DISABLE_CONTROL=false` 和 `GPU_CORE_UTILIZATION_POLICY=default`，覆盖镜像中可能关闭显存或算力限制的同名 `ENV`。当前单 GPU 合同把容器内设备索引固定为 `0`；CLI 同时设置全局显存、算力限制和同值的 `CUDA_DEVICE_MEMORY_LIMIT_0`、`CUDA_DEVICE_SM_LIMIT_0`，避免镜像中的 per-device `ENV` 优先覆盖全局值。`no-new-privileges` 会阻止 setuid、setgid 和文件 capability 在后续 `execve` 中授予新权限，避免 glibc 因这类权限提升忽略 `LD_PRELOAD`。原 Docker argv 的值和相对顺序保持不变。用户已设置以下内容时，CLI 会拒绝执行，避免静默覆盖：

- `--gpus`、非 `nvidia` 的 `--runtime` 或启用状态的 `--privileged`；
- 非 `linux/amd64` 的 `--platform`；
- 显式关闭 `no-new-privileges` 的 `--security-opt`；
- `--detach`、缺少 `--rm` 或非 `no` 的 restart policy；
- 所有调用方提供的 `--device`，或可能授权 NVIDIA 字符设备的 `--device-cgroup-rule`；
- HAMi Core 使用的环境变量（包括会关闭隔离的 `CUDA_DISABLE_CONTROL` 和会跳过算力限制的 `GPU_CORE_UTILIZATION_POLICY`），或不同时包含 `compute` 和 `utility` 的 `NVIDIA_DRIVER_CAPABILITIES`；
- 暴露 NVIDIA 设备、`--volumes-from`，或与 `/usr/local/vgpu/libvgpu.so`、`/tmp/hami-lite`、`/tmp/vgpulock` 重叠的挂载；
- `io.hami.lite.*` 管理标签。

`--env-file` 和 `--label-file` 只接受普通文件。真实执行时，CLI 校验并把 Docker argv 中的文件路径替换为固定快照。检查范围包括 `--label-file` 中不含 `=` 的键。FIFO、设备文件和其他特殊文件会被拒绝。

当前 PoC 不支持远端 Docker daemon、detached/restartable 容器、rootless Docker 或 musl 镜像，也不提供多容器容量账本、超售控制、调度、抢占、崩溃恢复或宿主机级安全隔离。多个容器的声明总量可能超过物理 GPU 容量，受信任的容器进程也可以绕过 `LD_PRELOAD`。这些场景应使用完整 HAMi 或 HAMi Enterprise。详细边界见 [架构说明](docs/architecture.md)。
