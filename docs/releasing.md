# hami-cli 发布维护说明

本文面向维护 `hami-cli` 发布工作流的开发者，说明发布通道、制品合同、仓库配置和失败边界。CLI 安装和使用方式见仓库根目录 [README](../README.md)。

## 发布通道

| 通道 | 触发方式 | 版本目录和镜像标签 | 可变性 |
| --- | --- | --- | --- |
| 稳定版 | 推送 `vX.Y.Z` 标签 | `vX.Y.Z` | 同一版本可在 GitHub Release、OSS、S3 和 GHCR 中更新 |
| 预发布版 | 推送 `vX.Y.Z-rc.1` 等预发布标签 | 标签原名 | 同一版本可在 GitHub Release、OSS、S3 和 GHCR 中更新 |
| latest | `main` 更新 | `latest` | 后续发布会覆盖 |
| 分支版 | 从 `main` 手动运行 Rolling Release | `branch-<slug>` | 同名分支版本会覆盖 |

版本标签必须符合 SemVer，且标签提交必须已经进入 `main`。为使标签可直接用作镜像标签和安装版本，标签最长为 128 个字符，预发布标识符使用小写字母、数字和连字符，省略 `+build` 元数据。全数字标识符遵循 SemVer 的前导零限制。

分支版输入必须是仓库中已有的远端分支。工作流将分支名转换为小写安全字符。原名称包含 `/`、大写字符或其他不安全字符，或者超过长度限制时，工作流会在版本末尾附加短哈希。

`latest` 和手动滚动发布使用不同的固定并发组。所有手动滚动发布会跨分支串行执行，避免将未经截断的分支名写入 GitHub Actions 并发组，也避免不同运行的多渠道分发相互穿插。

手动发布指定分支：

```bash
gh workflow run rolling-release.yaml --ref main -f branch=feature/my-change
```

## 发布制品

GoReleaser 为每个受支持的平台生成 `hami-cli_<version>_<os>_<arch>.tar.gz` 和同名的 `.sha256` 文件。每个归档只包含对应的可执行文件，Windows 的文件名为 `hami-cli.exe`。工作流在上传前校验平台、版本、归档内容和 SHA-256。

| 系统 | 架构 |
| --- | --- |
| Linux | `amd64`、`arm64` |
| macOS | `amd64`、`arm64` |
| Windows | `amd64` |

稳定版和预发布版均通过公开的 [GitHub Releases](https://github.com/dynamia-ai/hami-cli/releases) 提供五个归档、五个 `.sha256` 文件、汇总校验文件 `checksums.txt` 和 `image-digest.txt`。下载全部归档后，可以在同一目录运行 `sha256sum -c checksums.txt` 批量校验。

GitHub 为新 Release 草稿生成发布说明。工作流校验每个 GitHub 资产的 SHA-256 后公开草稿。GitHub 根据版本和 Release 创建时间选择标记为 **Latest** 的稳定版，预发布版显示为 **Pre-release**。滚动通道的 `latest` 是对象存储目录和 GHCR 镜像标签。

OSS 和 S3 同样发布平台归档和逐文件校验值。标签发布还向两个后端提供 `image-digest.txt`。所有通道均构建并发布支持 `linux/amd64` 和 `linux/arm64` 的 GHCR 镜像。

`main` 的 `latest` 发布还会基于仓库根目录的 `install.sh` 生成两份后端专用脚本，并分别写入两个公开下载前缀的 `install.sh`。OSS 脚本默认从 OSS 下载，R2 脚本默认从 R2 下载；主后端失败或校验不通过时，两者会互相回退。安装脚本本身是可变入口，默认安装 `latest`，并在运行时下载对应平台归档和 `.sha256` 校验文件，再调用二进制内置的 Skill 安装器。手动触发的分支版不会覆盖公共安装脚本。

两个发布工作流均设置 `DOCKER_BUILD_RECORD_UPLOAD: "false"`。`docker/build-push-action` 继续生成 Docker build summary，但不上传 `.dockerbuild` Actions artifact。

两个发布工作流都只构建一次，再通过临时 Actions artifacts 将制品传给各分发 job：`release-binaries-<version>` 包含平台归档、校验文件以及标签发布的 `image-digest.txt`，`release-image-<version>` 包含 OCI 镜像归档。二进制 artifact 只由 OSS、S3、GitHub Release 和 Summary 的相关 job 下载，镜像 artifact 只由 GHCR job 下载，避免各渠道重复构建或传递不需要的大文件。

标签发布在构建后并行分发到 GitHub、OSS、S3 和 GHCR。每个对象存储 job 单独校验本渠道的凭据和公开地址。GitHub Release 依赖构建得到的镜像 digest，GHCR 的上传和验证由独立 job 执行。滚动发布在分发前集中校验对象存储配置、二进制载荷和公共安装脚本。

所有渠道发布并校验成功后，workflow run summary 列出以下链接和引用。Cleanup job 随后删除临时载荷。分发失败时，可以在 1 天的 Actions artifact 保留期内重跑失败的 job。临时载荷过期后，重跑整个 workflow。

- GitHub Release 和汇总校验文件，仅标签发布提供。
- 每个对象存储文件的 OSS 和 S3 下载链接。
- `latest` 的一键安装脚本链接。
- GHCR 标签引用、不可变 digest 引用和 digest。

## 重试发布

推送版本标签前，确认版本号和目标提交。发布遇到后端故障时，修复后重跑原有标签的发布任务。GitHub 会沿用已有发布说明，并重用匹配的资产。草稿中的缺失或不匹配资产会在公开前补齐。

OSS 和 S3 允许覆盖同一版本路径下的同名对象，使用要求客户端重新验证的 `no-cache` 缓存策略。OSS Bucket 开启版本控制时保留历史版本，公开下载链接默认指向当前对象。

GitHub Release 允许通过 `--clobber` 更新已发布的同名资产。该操作先删除旧资产，再上传新资产。开启 GitHub 不可变发布时，已发布资产受 GitHub 保护。GHCR 允许更新版本标签，使用者可通过 `image@sha256:...` 固定镜像。

各分发渠道独立更新。某个渠道失败时，其他渠道可能已经提供新内容。重新运行失败任务后，查看公开回读结果和 workflow run summary，确认各渠道均已完成更新。

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

Pull Request 和 `main` 的 CI 使用仓库源码和公开 Go 模块构建。云存储访问凭据仅用于发布任务。

GHCR package 的写权限只能授予本仓库发布 workflow。其他主体拥有写权限时，可以在 workflow 完成后将已验证的稳定标签指向其他镜像。稳定交付必须保留并使用 `image@sha256:...` 引用，不能只依赖标签。

## 本地验证

Pull Request、`main` 和发布工作流共享仓库检查合同。提交发布相关变更前运行：

```bash
make ci
actionlint .github/workflows/*.yaml
```

涉及 GoReleaser 配置时，检查配置并构建本地 snapshot。

```bash
make release-check
make package-release VERSION=branch-release-check
```
