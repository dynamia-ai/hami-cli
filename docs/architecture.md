# hami-cli 架构说明

## 目标

`hami-cli` 同时承担两类入口：

- 管理 HAMi Enterprise 的 Kubernetes 资源，例如物理 GPU 资源池。
- 在非 Kubernetes 场景中，根据厂商插件为 Docker 命令注入 HAMi Core 所需参数。

项目只发布一个 `hami-cli` 二进制。厂商插件随主程序编译和发布，不要求用户安装 `hami-nvidia`、`hami-ascend` 等独立 CLI，也不在首版加载任意第三方动态代码。

## 当前状态

当前代码仅包含从 `hami-enterprise/cmd/hami-cli` 迁入的资源池命令。迁移保留以下行为：

- kubeconfig、上下文和命名空间参数；
- `ResourcePool`、`GPUDevice` 和 `ResourcePoolBinding` 的管理命令；
- YAML、JSON、名称和表格输出；
- `GPUDevice` 更新时的资源版本前置条件和乐观锁；
- 作为 kubectl 插件执行时的命令显示名称；
- 独立版本信息。

厂商运行时插件仍处于设计阶段。

## 目标命令模型

厂商运行时能力计划收敛到统一入口：

```text
hami-cli run --vendor VENDOR [HAMi 参数] -- docker run [Docker 参数] IMAGE [COMMAND] [ARG...]
```

命令形式将在 NVIDIA 插件 PoC 中验证后固定。当前文档中的命令只表示设计方向，不是已经可用的接口。

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
