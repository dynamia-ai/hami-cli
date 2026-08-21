# hami-cli

`hami-cli` 是 HAMi 的统一命令行入口。当前版本已从 `hami-enterprise/cmd/hami-cli` 迁入资源池管理能力；Docker 场景下的厂商运行时插件尚未实现。

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

项目发布到 GitHub 并创建版本标签后，可使用以下形式安装：

```bash
GOPRIVATE=github.com/dynamia-ai/* GOWORK=off \
  go install github.com/dynamia-ai/hami-cli@VERSION
```

将 `VERSION` 替换为已发布版本。当前本地仓库尚未配置远端，因此本文不声明 `@latest` 已可用。

## 厂商插件方向

项目只发布一个用户侧 `hami-cli` 二进制。NVIDIA、Ascend 等厂商适配将作为编译进同一二进制的厂商插件接入，不为每个厂商维护独立 CLI。

厂商插件负责设备发现、宿主机前置检查、Core 定位、Docker 参数注入和临时状态清理。主程序负责统一命令合同、参数分隔、`--dry-run` 输出、进程退出码和信号处理。

首个厂商插件开始实现前，需要先根据已经验证的 NVIDIA Docker PoC 固定插件合同。当前迁移不包含 Docker 参数注入，也不声明 NVIDIA 或 Ascend 已受支持。详细边界见 [架构说明](docs/architecture.md)。

## 迁移说明

当前资源池实现来自 `hami-enterprise` 的 `epic/resource-pool-manager` 分支。迁移阶段保留原命令、输出和并发更新语义，并继续依赖独立发布的 `pkg/resourcepool/api` 模块。

从 `hami-enterprise` 删除旧目录、切换发布流水线和发布首个独立版本属于后续工作。完成这些操作前，需要先验证新仓库的独立构建、安装和发布流程。

## License

本项目使用 [Apache License 2.0](LICENSE)。
