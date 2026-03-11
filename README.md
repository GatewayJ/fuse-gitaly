# opencsg-fuse-gitaly

> Copyright (c) 2025 OpenCSG. SPDX-License-Identifier: MIT

将 Gitaly 仓库挂载为本地 FUSE 文件系统，支持 ls、cd、tree、mkdir、cat、vim、mv 等常用命令。

## 技术栈

- Go
- [bazil.org/fuse](https://bazil.org/fuse)
- [gitlab.com/gitlab-org/gitaly/v16](https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/v16)

## Makefile

```bash
make fmt        # 格式化代码
make test       # 运行测试
make build      # 编译
make gitaly-up  # 启动本地 Gitaly (Docker)
make gitaly-down # 停止 Gitaly
make run-info   # 展示运行所需信息
make help       # 查看所有目标
```

## 编译

```bash
make build
# 或
go build -o gitaly-fuse ./cmd/gitaly-fuse
```

## 运行

```bash
./gitaly-fuse -gitaly localhost:8075 -storage default -repo @hashed/ab/cd/abcd1234... /mnt/git
```

### 参数

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| -gitaly | GITALY_ADDRESS | Gitaly 地址 (如 localhost:8075 或 unix:///path/to/socket) |
| -storage | GITALY_STORAGE | 存储名称，默认 default |
| -repo | GITALY_REPO | 仓库相对路径 (如 @hashed/ab/cd/abcd...) |
| -branch | GITALY_BRANCH | 挂载分支，空则使用默认分支 |
| -user | GITALY_USER | 提交作者名 |
| -email | GITALY_EMAIL | 提交作者邮箱 |
| -token | GITALY_TOKEN | Bearer token 用于 gRPC 认证 |

### 性能配置

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| -cache | GITALY_CACHE | 是否启用缓存，默认 true |
| -cache-max-entries | GITALY_CACHE_MAX_ENTRIES | 缓存最大条目数，默认 1000 |
| -cache-ttl | GITALY_CACHE_TTL | 缓存过期时间，默认 5m |
| -cache-max-blob-size | GITALY_CACHE_MAX_BLOB_SIZE | 超过此大小的 blob 不缓存（字节），0=全部缓存，默认 1MB |
| -grpc-timeout | GITALY_GRPC_TIMEOUT | gRPC 调用超时，默认 30s |

### 示例

```bash
# 使用环境变量
export GITALY_ADDRESS=localhost:8075
export GITALY_REPO=@hashed/ab/cd/abcd1234...
./gitaly-fuse /mnt/git

# 指定分支
./gitaly-fuse -gitaly localhost:8075 -repo my-project -branch main /mnt/git

# 使用 token 认证
./gitaly-fuse -gitaly localhost:8075 -repo my-project -token "your-token" /mnt/git

# 禁用缓存（调试用）
./gitaly-fuse -cache=false -gitaly localhost:8075 -repo my-project /mnt/git

# 调整缓存与超时
./gitaly-fuse -cache-max-entries 2000 -cache-ttl 10m -grpc-timeout 60s -gitaly localhost:8075 -repo my-project /mnt/git
```

## 支持的操作

- **ls** - 列出目录
- **cd** - 切换目录
- **tree** - 树形展示
- **mkdir** - 创建目录
- **cat** - 读取文件
- **vim** - 编辑并保存文件
- **mv** - 移动/重命名文件或目录

## 集成测试

```bash
make integration-test   # 完整测试 (需 Docker, 首次会拉取 Gitaly 镜像)
```

若已有 Gitaly 在运行 (如 GDK、GitLab):
```bash
GITALY_SKIP_START=1 make integration-test
```

## License

MIT License. See [LICENSE](LICENSE) for details.

## 注意事项

- 写操作依赖 Gitaly/GitLab 侧权限，无权限时写会失败
- 需确保挂载点存在且为空
- 卸载: `fusermount -u /mnt/git` 或 Ctrl+C
