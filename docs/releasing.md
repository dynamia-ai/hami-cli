# hami-cli 发布维护说明

本文面向维护 `hami-cli` 发布工作流的开发者，说明发布通道、制品合同、仓库配置和失败边界。CLI 安装和使用方式见仓库根目录 [README](../README.md)。

## 发布通道

| 通道 | 触发方式 | 版本目录和镜像标签 | 可变性 |
| --- | --- | --- | --- |
| 稳定版 | 推送 `vX.Y.Z` 标签 | `vX.Y.Z` | 同一版本可在 OSS、S3、GitHub Release 和 GHCR 中覆盖 |
| latest | `main` 更新 | `latest` | 后续发布会覆盖 |
| 分支版 | 从 `main` 手动运行 Rolling Release | `branch-<slug>` | 同名分支版本会覆盖 |

稳定版标签必须符合语义化版本格式，且标签提交必须已经进入 `main`。分支版输入必须是仓库中已有的远端分支。工作流会将分支名转换为小写安全字符；原名称包含 `/`、大写字符、其他不安全字符或长度超过限制时，版本末尾会附加短哈希。

手动发布指定分支：

```bash
gh workflow run rolling-release.yaml --ref main -f branch=feature/my-change
```

## 发布制品

GoReleaser 生成五个平台归档，以及与每个归档同名的 `.sha256` 校验文件：

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

每个平台归档只包含对应平台的 `hami-cli` 可执行文件。发布流程在上传前检查归档内容。如果归档包含其他文件，发布流程停止。

稳定版还发布 `image-digest.txt`。所有通道都会构建并发布 `linux/amd64`、`linux/arm64` 多架构 GHCR 镜像。

发布任务会将二进制制品写入两个公开下载后端：

- 阿里云 OSS
- S3 兼容对象存储

稳定版会额外创建 GitHub Release 副本。访问该副本需要登录 GitHub，并具有私有仓库的读取权限。

`main` 的 `latest` 发布还会基于仓库根目录的 `install.sh` 生成两份后端专用脚本，并分别写入两个公开下载前缀的 `install.sh`。OSS 脚本默认从 OSS 下载，R2 脚本默认从 R2 下载；主后端失败或校验不通过时，两者会互相回退。安装脚本本身是可变入口，默认安装 `latest`，并在运行时下载对应平台归档和 `.sha256` 校验文件，再调用二进制内置的 Skill 安装器。手动触发的分支版不会覆盖公共安装脚本。

两个发布工作流均设置 `DOCKER_BUILD_RECORD_UPLOAD: "false"`。`docker/build-push-action` 继续生成 Docker build summary，但不上传 `.dockerbuild` Actions artifact。

两个发布工作流都只构建一次，再通过临时 Actions artifacts 将制品传给各分发 job：`release-binaries-<version>` 包含平台归档、校验文件以及稳定版的 `image-digest.txt`，`release-image-<version>` 包含 OCI 镜像归档。二进制 artifact 只由 OSS、S3、GitHub Release 和 Summary 的相关 job 下载，镜像 artifact 只由 GHCR job 下载，避免各渠道重复构建或传递不需要的大文件。

分发前置 job 会先集中检查所有渠道的凭据和公开下载地址，配置不完整时不会启动任一发布操作。OSS、S3、GHCR 分发随后并行执行，稳定版还会并行创建 GitHub Release 副本。只有所有渠道均发布并校验成功后，Summary job 才会写入成功摘要。Cleanup job 使用 `always()` 等待所有 artifact 消费者结束，因此任一分发 job 失败时仍会删除两个临时 artifacts。两者的保留期仍设置为 1 天，仅作为 workflow 被外部取消或 GitHub artifact 删除 API 不可用时的兜底；Actions artifact 不是公开分发端点。

发布成功后，workflow run summary 会列出：

- 每个公开文件的 OSS 和 S3 下载链接；
- `latest` 发布的一键安装脚本 OSS 和 S3 链接；
- GHCR 标签引用、不可变 digest 引用和 digest。

## 稳定版覆盖合同

稳定版允许更新同一版本路径、GitHub Release 资产和 GHCR 标签。发布者应在推送标签前确认版本号和目标提交，尽量避免复用已发布版本。发布工作流不会阻止覆盖已有版本。

- OSS 和 S3 允许覆盖同一版本路径下的同名对象。对象使用 `no-cache` 缓存策略，客户端需要重新验证对象内容。
- OSS Bucket 开启版本控制时，OSS 保留历史版本；不带 `versionId` 的公开下载链接指向当前版本。
- GitHub Release 使用 `--clobber` 更新同名资产。该操作会先删除旧资产再上传新资产，不是原子替换；上传失败时，发布工作流停止并保留失败状态。
- GHCR 版本标签会更新到本次构建的镜像。发布后仍会验证标签对应的 digest、版本和源码 revision；使用者需要不可变引用时，应使用 `image@sha256:...`。

滚动版的 `latest` 和分支版本在 OSS、S3 和 GHCR 中同样允许更新。

各发布后端的更新不构成原子事务。如果部分后端更新后任务失败，各通道可能暂时提供不同内容。排除故障后，重新运行同一标签的发布任务，使各后端收敛到本次构建内容。发布任务仅在全部上传和公开回读校验完成后写入成功 Summary。

## 公开可用性验证

对象存储上传成功不等于公开地址可以下载。稳定版和滚动版将 OSS 和 S3 回读显示为独立步骤，并使用同一个脚本逐个下载文件、比较 SHA-256。`latest` 发布还会回读根路径的 `install.sh` 并与本次构建载荷比较。脚本验证不带缓存绕过参数的正式下载 URL。下载失败或内容不一致时，脚本最多尝试 8 次，每次间隔 5 秒。验证仍然失败时，发布任务停止，也不会写入成功 Summary。

每个文件验证成功后，日志记录后端名称、文件名、SHA-256、HTTP 状态和尝试次数。验证失败时，日志和 workflow run summary 记录以下信息：

- 后端名称、文件名和完整 URL；
- 期望 SHA-256 和实际 SHA-256；
- `curl` 退出码、HTTP 状态、下载字节数和请求耗时；
- `Age`、`Cache-Control`、`CF-Cache-Status`、`ETag`、`Last-Modified` 等缓存诊断响应头。

如果实际 SHA-256 与期望值不同，同时响应包含 `CF-Cache-Status: HIT` 和旧的 `Last-Modified`，先检查 CDN 缓存。不要只给验证 URL 添加随机查询参数；这样不能证明正式下载 URL 已经提供新文件。

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

OSS 发布身份需要读写对象和设置对象 ACL，S3 发布身份需要读写对象。发布工作流不检查 Bucket 版本控制状态，稳定版和滚动版均允许覆盖同名对象。

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
