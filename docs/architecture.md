# hami-cli 架构说明

## 目标

`hami-cli` 同时承担两类入口：

- 管理 HAMi Enterprise 的 Kubernetes 资源，例如物理 GPU 资源池。
- 在非 Kubernetes 场景中，根据厂商插件为 Docker 命令注入 HAMi Core 所需参数。

项目只发布一个 `hami-cli` 二进制。厂商插件随主程序编译和发布，不要求用户安装 `hami-nvidia`、`hami-ascend` 等独立 CLI，也不在首版加载任意第三方动态代码。

## 当前状态

当前代码包含从 `hami-enterprise/cmd/hami-cli` 迁入的资源池命令，以及首个 NVIDIA Docker 运行 PoC。资源池迁移保留以下行为：

- kubeconfig、上下文和命名空间参数；
- `ResourcePool`、`GPUDevice` 和 `ResourcePoolBinding` 的管理命令；
- YAML、JSON、名称和表格输出；
- `GPUDevice` 更新时的资源版本前置条件和乐观锁；
- 作为 kubectl 插件执行时的命令显示名称；
- 独立版本信息。

NVIDIA PoC 已实现以下共享合同：

- 只接受 `--` 后的完整 `docker run` argv，不拼接 shell 字符串；
- 在 `docker run` 后注入参数，保留用户 argv 的值和相对顺序；
- 校验 Core 版本和 SHA-256 摘要；
- 校验 Core 是 Linux/AMD64 使用的可加载 ELF64 x86-64 共享库；
- 真实执行时，固定并挂载通过校验的 Core 字节快照；
- 固定并校验 `--env-file` 和 `--label-file`，避免 Docker 读取校验后的路径替换；
- 检测会覆盖 GPU、Core 环境变量、挂载路径和管理标签的 Docker 参数；
- `--dry-run` 输出经过 POSIX shell quoting 的可复制命令，并使用可供多个容器共享的 Core bind mount SELinux relabel 选项；
- 实际执行通过 `exec` 替换当前进程，保留 Docker 的标准输入输出、退出码和信号。

当前只支持 Linux/AMD64、glibc 镜像、rootful 本机 Docker、NVIDIA、单物理 GPU、前台 `--rm` 容器和受信任工作负载。远端 Docker daemon、detached/restartable 容器、Ascend、多 GPU、MIG、ARM64、rootless、Windows 和 macOS 尚未实现或验证。

## 目标命令模型

厂商运行时能力使用统一入口：

```text
hami-cli run --vendor VENDOR [HAMi 参数] -- docker run [Docker 参数] IMAGE [COMMAND] [ARG...]
```

NVIDIA 插件当前使用以下参数：

- `--device`：一个 GPU 索引或 `GPU-...` UUID；
- `--memory`：正整数显存上限，支持字节、KiB、MiB 和 GiB；
- `--compute`：`1` 到 `100` 的 SM 利用率上限；
- `--core`、`--core-version` 和 `--core-sha256`：Core 路径、版本和完整性信息；
- `--dry-run`：只输出最终命令，不执行 Docker，也不检查 Docker endpoint 或本机 GPU。

这些参数固定了首个 NVIDIA PoC 合同。新增厂商时仍需独立验证厂商参数和注入方式，不能直接外推 NVIDIA 行为。

## 插件边界

主程序负责：

- 解析统一参数和 `--` 分隔符；
- 选择厂商插件；
- 保留 Docker argv 的值和顺序；
- 输出可审阅、可复制的 `--dry-run` 命令；
- 执行最终进程并保留退出码和信号；
- 只清理由本次调用创建的临时状态。

厂商插件负责：

- 检查厂商驱动、设备、宿主库和 Core 制品；
- 校验厂商资源参数；
- 生成设备节点、挂载、环境变量、标签和运行时参数；
- 检测会破坏 Core 注入合同的 Docker 参数冲突；
- 提供厂商特有的诊断信息。

NVIDIA 插件在实际执行前解析 `DOCKER_CONTEXT`、`DOCKER_HOST` 和当前 Docker context，确认 Docker 将使用本机 `unix://` endpoint，并通过 `nvidia-smi` 校验所选 GPU。真实执行时，插件先把 Core 复制到权限为 `0700` 的绝对临时目录，再从同一个权限为 `0444` 的只读快照完成 SHA-256 和 ELF 校验。插件拒绝位于 `noexec` 文件系统的临时目录。Docker 使用私有 `Z` label 把该快照只读挂载到 `/usr/local/vgpu/libvgpu.so`，使 SELinux 容器可以加载快照。CLI 不会在校验后重新按原始路径打开 Core，避免原始路径内容在校验与挂载之间变化。

同一快照机制用于 `--env-file` 和 `--label-file`。插件先复制文件，再校验快照内容，并把 Docker argv 中的原始路径替换为快照路径。dry-run 本身不创建快照，也不修改原始文件的 SELinux label。dry-run 输出使用 `--volume <原始路径>:/usr/local/vgpu/libvgpu.so:ro,z`；执行该输出时，Docker 会对原始 Core 应用可供多个容器共享的 SELinux relabel，避免并发执行引用同一 Core 的命令时互相替换私有 MCS label。该命令不具备真实执行路径的字节快照固定保证。

清理辅助进程通过继承的生命周期管道等待前台 Docker 客户端退出，随后删除快照目录。插件要求 `--rm`，并拒绝 `--detach` 和非 `no` 的 restart policy，使容器生命周期不超过快照生命周期。

插件设置 `LD_PRELOAD` 与 `CUDA_REDIRECT`，并保证 `NVIDIA_DRIVER_CAPABILITIES` 至少包含 `compute` 和 `utility`。插件还设置 `no-new-privileges=true`，阻止 setuid、setgid 和文件 capability 在后续 `execve` 中授予新权限，避免 glibc 因这类权限提升进入 secure-execution 模式并忽略 `LD_PRELOAD`。调用方不能关闭该选项。显存、算力和共享缓存分别使用 `CUDA_DEVICE_MEMORY_LIMIT`、`CUDA_DEVICE_SM_LIMIT` 和 `CUDA_DEVICE_MEMORY_SHARED_CACHE`。缓存目录 `/tmp/hami-lite` 与锁目录 `/tmp/vgpulock` 使用 Docker tmpfs，由容器生命周期回收，不在宿主机保留 CLI 临时状态。

Docker 参数检查会拒绝所有调用方提供的 `--device`，因为 CLI 无法保证 Docker daemon 打开的设备路径仍指向校验时的 inode。插件也会拒绝可能授权 NVIDIA 字符设备的 device cgroup 规则。`--env-file` 和 `--label-file` 必须引用普通文件；插件在读取文件前拒绝 FIFO、设备文件和其他特殊文件，并检查 label 文件中的裸键。插件还拒绝无法静态检查继承挂载目标的 `--volumes-from`，并把 `CUDA_DISABLE_CONTROL` 作为 HAMi Core 受保护环境变量。

插件不负责本机容量账本、多容器调度、超售控制、抢占或崩溃恢复。这些能力属于 HAMi 或 HAMi Enterprise 控制面。

## 依赖方向

```text
cmd/root
├── resource-pool commands ──> Kubernetes client + resource-pool API
└── run command
    ├── shared argv/execution contract
    └── vendor plugin
        ├── NVIDIA
        └── Ascend
```

主程序持有最小插件接口并显式注册厂商实现。厂商插件不能通过 `init` 隐式注册，也不能反向依赖命令装配代码。

## 迁移阶段

- 建立独立 Go 模块并原样迁入资源池命令和测试。
- 在新仓库验证独立构建、安装和发布。
- 从 `hami-enterprise` 删除旧 CLI 源码，保留独立发布的资源池 API。
- 根据真实 NVIDIA Docker PoC 固定共享运行合同和 NVIDIA 插件。
- 在独立验证 Ascend 注入合同后接入 Ascend 插件。

迁移完成前，不应在两个仓库同时修改 CLI 行为。进入删除阶段后，`hami-enterprise` 只维护资源池服务端和公开 API，CLI 行为由本仓库维护。
