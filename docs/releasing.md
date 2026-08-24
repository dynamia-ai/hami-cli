# hami-cli 发布维护说明

本文面向维护 `hami-cli` 发布工作流的开发者，说明发布通道、制品合同、仓库配置和失败边界。CLI 安装和使用方式见仓库根目录 [README](../README.md)。

## 发布通道

| 通道 | 触发方式 | 版本目录和镜像标签 | 可变性 |
| --- | --- | --- | --- |
| 稳定版 | 推送 `vX.Y.Z` 标签 | `vX.Y.Z` | OSS、S3 和 GitHub Release 资产不可覆盖；GHCR 标签本身可变 |
| latest | `main` 更新 | `latest` | 后续发布会覆盖 |
| 分支版 | 从 `main` 手动运行 Rolling Release | `branch-<slug>` | 同名分支版本会覆盖 |

稳定版标签必须符合语义化版本格式，且标签提交必须已经进入 `main`。分支版输入必须是仓库中已有的远端分支。工作流会将分支名转换为小写安全字符；原名称包含 `/`、大写字符、其他不安全字符或长度超过限制时，版本末尾会附加短哈希。

手动发布指定分支：

```bash
gh workflow run rolling-release.yaml --ref main -f branch=feature/my-change
```

## 发布制品

GoReleaser 生成五个平台归档和 `checksums.txt`：

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

稳定版还发布 `image-digest.txt`。所有通道都会构建并发布 `linux/amd64`、`linux/arm64` 多架构 GHCR 镜像。

发布任务会将二进制制品写入两个公开下载后端：

- 阿里云 OSS
- S3 兼容对象存储

稳定版会额外创建 GitHub Release 副本。访问该副本需要登录 GitHub，并具有私有仓库的读取权限。滚动版使用 Actions artifact 在 build 和 publish job 之间传递制品；发布成功后立即删除，发布失败时最多保留 1 天用于诊断。Actions artifact 不是公开分发端点，稳定版不上传 Actions artifact。

发布成功后，workflow run summary 会列出：

- 每个公开文件的 OSS 和 S3 下载链接；
- GHCR 标签引用、不可变 digest 引用和 digest。

## 不可变发布合同

稳定版发布遵循以下规则：

- OSS 使用服务端禁止覆盖参数写入对象。
- S3 使用 `If-None-Match: *` 条件请求写入对象。
- GitHub Release 不覆盖内容不同的同名资产。
- 同一提交生成的相同内容可以在失败后继续发布。
- 已存在对象、Release 资产或 GHCR 标签与本次内容、digest、源码 revision 不一致时，工作流停止。
- GHCR 不提供服务端不可变标签。发布时验证标签对应的 digest 和镜像 revision；使用者需要不可变引用时，应使用 `image@sha256:...`。

滚动版的 `latest` 和分支版本是可变通道，不适合作为不可变交付合同。

## 公开可用性验证

对象存储上传成功不等于公开地址可以下载。发布工作流会从两个公开下载前缀逐个下载文件，并与本地发布制品进行字节比较。任一后端下载失败或内容不一致时，发布任务停止，也不会写入成功 Summary。

公开下载前缀会先移除末尾的 `/`。回读校验和 Summary 生成使用同一个规范化后的 URL，避免验证地址和展示地址不一致。

GHCR 验证会检查清单 digest，以及 `linux/amd64`、`linux/arm64` 镜像配置中的版本和源码 revision 标签。

## GitHub Actions 配置

仓库需要以下 Actions secrets：

- `HAMI_ENTERPRISE_REPO_TOKEN`：对 `github.com/dynamia-ai/hami-enterprise` 具有只读 `Contents` 权限，用于解析私有 Go 模块。
- `ALIYUN_OSS_ACCESS_KEY_ID`、`ALIYUN_OSS_ACCESS_KEY_SECRET`：OSS 访问凭据。
- `ALIYUN_OSS_BUCKET`、`ALIYUN_OSS_ENDPOINT`、`ALIYUN_OSS_REGION`：OSS Bucket、API 端点和区域。
- `S3_ACCESS_KEY_ID`、`S3_SECRET_ACCESS_KEY`：S3 兼容存储访问凭据。
- `S3_BUCKET`、`S3_ENDPOINT`、`S3_REGION`：S3 兼容存储 Bucket、API 端点和区域。

仓库还需要以下 Actions variables：

- `ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL`：包含 `/hami-cli` 的 OSS 公开下载前缀。
- `S3_PUBLIC_DOWNLOAD_BASE_URL`：包含 `/hami-cli` 的 S3 兼容存储公开下载前缀。

OSS 发布身份需要读取 Bucket 版本控制状态、读写对象和设置对象 ACL。稳定版发布要求 OSS Bucket 关闭版本控制；状态为 `Enabled` 或 `Suspended` 时，工作流停止。

使用 Cloudflare R2 时：

- `S3_ENDPOINT` 使用 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`；
- `S3_REGION` 使用 `auto`；
- API Token 至少具有目标 Bucket 的 Object Read & Write 权限；
- Bucket 需要启用公开自定义域名或 `r2.dev` 地址；
- R2 不支持 S3 对象 ACL，发布脚本不会设置 `public-read`。

## 凭据边界

Pull Request 和 `main` 的 CI 都需要读取私有资源池 API 模块。`hami-cli` 仓库的 write 权限不得授予无权读取 `hami-enterprise` 的用户，否则同仓库 PR 代码执行边界会扩大私有模块访问范围。

调整任一仓库成员权限时，需要重新核对这一条件。如果两个仓库的写入和读取边界不再一致，应先停用 Pull Request 中使用私有模块 token 的 job，再改用具备审批保护的凭据边界。

GHCR package 的写权限只能授予本仓库发布 workflow。其他主体拥有写权限时，可以在 workflow 完成后将已验证的稳定标签指向其他镜像。稳定交付必须保留并使用 `image@sha256:...` 引用，不能只依赖标签。

## 本地验证

Pull Request、`main` 和发布工作流共享仓库检查合同。提交发布相关变更前运行：

```bash
make ci
actionlint .github/workflows/*.yaml
```

涉及 GoReleaser 配置时，再运行：

```bash
make release-check
```
