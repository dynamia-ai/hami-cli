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
