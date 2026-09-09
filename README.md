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
