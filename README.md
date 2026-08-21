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
- 目标镜像使用 glibc；Core 文件权限允许目标容器用户读取；
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

`--dry-run` 只生成经过 POSIX shell quoting 的最终命令，不检查 Docker endpoint 或本机 GPU。CLI 仍会校验 Core 文件的 SHA-256、ELF 架构和可加载共享库结构。dry-run 输出引用调用时校验的原始路径，不会为后续执行保留文件快照：

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

真实执行时，CLI 把 Core 字节复制到权限为 `0700` 的私有临时目录，再从同一个只读快照完成 SHA-256 和 ELF 校验并挂载到容器。即使原始路径随后被替换，Docker 读取的仍是通过校验的快照。清理辅助进程在 Docker 客户端退出后删除快照目录。

CLI 在 `docker run` 后插入 `--gpus`、Core 只读挂载、私有 tmpfs 状态目录、HAMi Core 环境变量、最低 NVIDIA driver capabilities 和可审计标签。原 Docker argv 的值和相对顺序保持不变。用户已设置以下内容时，CLI 会拒绝执行，避免静默覆盖：

- `--gpus`、非 `nvidia` 的 `--runtime` 或启用状态的 `--privileged`；
- 指向 NVIDIA 设备的 `--device`，或可能授权 NVIDIA 字符设备的 `--device-cgroup-rule`；
- HAMi Core 使用的环境变量，或不同时包含 `compute` 和 `utility` 的 `NVIDIA_DRIVER_CAPABILITIES`；
- 暴露 NVIDIA 设备，或与 `/usr/local/vgpu/libvgpu.so`、`/tmp/hami-lite`、`/tmp/vgpulock` 重叠的挂载；
- `io.hami.lite.*` 管理标签。

`--env-file` 和 `--label-file` 只接受普通文件。CLI 在调用 Docker 前检查文件内容，包括 `--label-file` 中不含 `=` 的键。FIFO、设备文件和其他特殊文件会被拒绝。

当前 PoC 不支持远端 Docker daemon、rootless Docker 或 musl 镜像，也不提供多容器容量账本、超售控制、调度、抢占、崩溃恢复或宿主机级安全隔离。多个容器的声明总量可能超过物理 GPU 容量，受信任的容器进程也可以绕过 `LD_PRELOAD`。这些场景应使用完整 HAMi 或 HAMi Enterprise。详细边界见 [架构说明](docs/architecture.md)。
