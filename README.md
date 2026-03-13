# opencsg-fuse-gitaly

> Copyright (c) 2025 OpenCSG. SPDX-License-Identifier: MIT

支持两种 FUSE 挂载模式，均支持 **ls、cd、tree、cat、vim** 及缓存：

- **gitaly**：将 Gitaly 仓库挂载为目录（支持 mkdir/mv 等写操作回写 Gitaly）
- **skills**：将技能 API 列表 + 本地 clone 目录挂载为目录（official/、user/ 下为各技能，读写落盘）

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

通过 **-mode** 区分挂载类型：`gitaly`（默认）或 `skills`。

### 模式一：Gitaly（-mode=gitaly）

```bash
./gitaly-fuse -mode=gitaly -gitaly localhost:8075 -storage default -repo test-repo.git /mnt/git
```

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| -mode | FUSE_MODE | 挂载模式，默认 gitaly |
| -gitaly | GITALY_ADDRESS | Gitaly 地址 |
| -storage | GITALY_STORAGE | 存储名称，默认 default |
| -repo | GITALY_REPO | 仓库相对路径 |
| -branch | GITALY_BRANCH | 分支，空则默认分支 |
| -user, -email | GITALY_USER, GITALY_EMAIL | 提交作者 |
| -token | GITALY_TOKEN | gRPC Bearer token |
| -cache | GITALY_CACHE | 是否启用缓存（tree/blob），默认 true |
| -cache-max-entries | GITALY_CACHE_MAX_ENTRIES | 缓存条目数，默认 1000 |
| -cache-ttl | GITALY_CACHE_TTL | 缓存 TTL，默认 5m |
| -cache-max-blob-size | GITALY_CACHE_MAX_BLOB_SIZE | 大于此大小的 blob 不缓存，0=全缓存 |
| -grpc-timeout | GITALY_GRPC_TIMEOUT | gRPC 超时，默认 30s |

### 模式二：Skills（-mode=skills）

挂载技能 API 列表 + 本地 clone 目录，根目录为 `official/` 与 `user/`，其下为技能名（首次访问时自动 clone），支持 **ls、cat、vim** 及技能列表缓存。

```bash
./gitaly-fuse -mode=skills -base-url https://api.example.com -skills-token YOUR_TOKEN /mnt/skills
```

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| -base-url | CSGHUB_API_BASE_URL | 技能 API 根地址 |
| -skills-token | CSGHUB_USER_TOKEN | Bearer token |
| -skills-user | CSGHUB_USER_NAME | 用户名（clone URL 认证） |
| -workspace | AGENTICHUB_WORKSPACE | 工作目录，默认 /root/.agentichub |
| -cache | GITALY_CACHE | 是否缓存技能列表，默认 true |
| -cache-max-entries | GITALY_CACHE_MAX_ENTRIES | 缓存条目数 |
| -cache-ttl | GITALY_CACHE_TTL | 技能列表缓存 TTL |

挂载后目录结构示例：

```
/mnt/skills/
├── official/
│   ├── skill-a/    # 对应 clone 到 workspace/skills/official/skill-a
│   └── skill-b/
└── user/
    └── my-skill/
```

### 通用示例

```bash
# Gitaly：环境变量
export GITALY_ADDRESS=localhost:8075
export GITALY_REPO=test-repo.git
./gitaly-fuse -mode=gitaly /mnt/git

# Skills：环境变量
export CSGHUB_API_BASE_URL=https://api.example.com
export CSGHUB_USER_TOKEN=xxx
./gitaly-fuse -mode=skills /mnt/skills

# 禁用缓存（调试）
./gitaly-fuse -cache=false -mode=gitaly -gitaly localhost:8075 -repo test-repo.git /mnt/git
```

## 技能同步（Skills Sync）— 对接 AgentHub/CSGHub API

若需对接 **技能列表 API** 并按脚本方式下载技能仓库（与 Python 脚本行为一致），可使用 `skills-sync`：

1. **GET** `{base_url}/api/v1/agent/skills` 拉取技能列表  
2. 将每个技能 **git clone** 到本地：`https://{user}:{token}@{base}/skills/{skill.path}.git`  
3. 落盘目录：`{workspace}/skills/official/` 与 `{workspace}/skills/user/`

### 编译 skills-sync

```bash
go build -o skills-sync ./cmd/skills-sync
```

### 环境变量

| 变量 | 说明 |
|------|------|
| `CSGHUB_API_BASE_URL` | API 根地址（如 `https://api.example.com`） |
| `CSGHUB_USER_TOKEN` | Bearer 认证 Token |
| `CSGHUB_USER_NAME` | 用户名，用于 clone URL 认证 |
| `AGENTICHUB_WORKSPACE` | 工作目录，默认 `/root/.agentichub` |

### 运行

```bash
export CSGHUB_API_BASE_URL=https://your-api.example.com
export CSGHUB_USER_TOKEN=your-token
export CSGHUB_USER_NAME=your-username
./skills-sync
```

- 若已存在成功标记文件（`.skills_initialized`），会直接跳过。  
- 同步完成后，技能在 `{workspace}/skills/official/<name>` 与 `{workspace}/skills/user/<name>` 下，可直接访问文件。  
- 实现位于 `internal/skillsclient`，可在其他 Go 代码中复用 `FetchSkills`、`CloneOrPull`、`SyncAll`。

---

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
