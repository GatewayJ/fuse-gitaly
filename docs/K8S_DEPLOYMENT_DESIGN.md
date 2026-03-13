# fuse-gitaly 在 Kubernetes 中的部署方案

> Copyright (c) 2025 OpenCSG. SPDX-License-Identifier: MIT

本文档描述 fuse-gitaly 在 Kubernetes 中以边车（Sidecar）模式运行、支持动态挂载多仓库的完整方案，以及相关注意事项。

---

## 1. 方案概述

### 1.1 架构选型

| 方案 | 优点 | 缺点 |
|------|------|------|
| **Pod 内直接运行** | 简单 | 主容器需 SYS_ADMIN，安全风险高 |
| **节点挂载 + CSI** | 多 Pod 共享挂载 | 需节点预配置，调度绑定复杂 |
| **边车模式** ✓ | 主容器无特权，部署简单，生命周期一致 | 每 Pod 一个 sidecar，资源略增 |

**推荐采用边车模式**：主应用保持无特权，FUSE 挂载由 sidecar 负责，通过共享 volume 暴露给主容器。

### 1.2 核心架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Pod                                                                     │
│                                                                          │
│  ┌──────────────────────────┐     ┌────────────────────────────────────┐ │
│  │ main-app (主应用)          │     │ gitaly-fuse (sidecar)             │ │
│  │ - 无 SYS_ADMIN             │     │ - SYS_ADMIN + /dev/fuse           │ │
│  │ - 普通用户运行              │     │ - 动态挂载多个 repo               │ │
│  │ - 读写 /mnt/git/*          │     │ - 监听挂载请求，按需挂载           │ │
│  └────────────┬─────────────┘     └──────────────┬─────────────────────┘ │
│               │                                   │                       │
│               └────────── emptyDir ───────────────┘                       │
│                      /mnt/git (共享 volume)                               │
│                      ├── repo-1/  (FUSE 挂载)                             │
│                      ├── repo-2/  (FUSE 挂载)                             │
│                      └── repo-N/  (FUSE 挂载)                             │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 动态挂载设计

### 2.1 mountPath 处理

Kubernetes 的 `volumeMounts.mountPath` 在 Pod 创建时固定，无法运行时修改。采用**单一路径 + 子目录**策略：

- **固定 mountPath**：`/mnt/git`（仅此一层）
- **动态子目录**：`/mnt/git/<repo-id>/` 作为各 repo 的 FUSE 挂载点

```
/mnt/git/                          ← 共享 volume 根目录（固定）
├── hashed-ab-cd-abcd1234/         ← 动态创建，FUSE 挂载 repo A
├── group-project/                 ← 动态创建，FUSE 挂载 repo B
└── proj-123/                     ← 动态创建，FUSE 挂载 repo C
```

### 2.2 子目录命名规则

| 原始 repo 路径 | 子目录名（示例） |
|----------------|------------------|
| `@hashed/ab/cd/abcd1234...` | `hashed-ab-cd-abcd1234` |
| `group/subgroup/project` | `group-subgroup-project` |
| `my-project` | `my-project` |

**命名要求**：替换 `/` 为 `-`，过滤非法字符（`..`、空等），保证路径安全。

### 2.3 动态挂载流程

```
1. 主应用需要 repo X
   └─> 创建空目录: mkdir /mnt/git/<repo-id>
   └─> 发送挂载请求（HTTP/gRPC/文件）

2. gitaly-fuse sidecar 收到请求
   └─> 校验目录存在且为空
   └─> fuse.Mount("/mnt/git/<repo-id>", repo=X, ...)
   └─> 挂载完成，通知主应用（可选）

3. 主应用访问 /mnt/git/<repo-id>/
   └─> 读写操作经 FUSE 透传到 Gitaly
```

### 2.4 主应用与 sidecar 的协作

主应用需具备：

- **等待就绪**：挂载前 `/mnt/git/<repo-id>` 不存在或为空，需轮询或等待 sidecar 完成
- **错误处理**：挂载失败、Gitaly 不可达时的降级逻辑
- **触发方式**：通过 HTTP API、Unix Socket 或约定文件触发 sidecar 挂载

---

## 3. Kubernetes 配置要点

### 3.1 核心配置（安全上下文、设备、共享 Volume）

以下配置需一并使用：sidecar 需 SYS_ADMIN、挂载 /dev/fuse、与主容器共享 emptyDir。若节点启用 AppArmor，可添加 annotations。

```yaml
spec:
  template:
    metadata:
      # 若节点启用 AppArmor，取消下面注释
      # annotations:
      #   container.apparmor.security.beta.kubernetes.io/gitaly-fuse: unconfined
    spec:
      containers:
        - name: main-app
          volumeMounts:
            - name: git-repo
              mountPath: /mnt/git
          # 主容器无需 securityContext，保持默认

        - name: gitaly-fuse
          securityContext:
            capabilities:
              add:
                - SYS_ADMIN
          volumeMounts:
            - name: fuse-device
              mountPath: /dev/fuse
            - name: git-repo
              mountPath: /mnt/git

      volumes:
        - name: fuse-device
          hostPath:
            path: /dev/fuse
            type: CharDevice
        - name: git-repo
          emptyDir: {}
```

### 3.2 优雅退出

```yaml
terminationGracePeriodSeconds: 30

# 可选：preStop 确保卸载
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "fusermount -u /mnt/git 2>/dev/null || true; for d in /mnt/git/*/; do fusermount -u \"$d\" 2>/dev/null || true; done"]
```

### 3.3 环境变量

| 变量 | 说明 |
|------|------|
| GITALY_ADDRESS | Gitaly 地址，如 `gitaly.gitlab.svc.cluster.local:8075` |
| GITALY_STORAGE | 存储名称，默认 `default` |
| GITALY_TOKEN | Bearer token（Gitaly 需认证时） |
| GITALY_USER / GITALY_EMAIL | 提交作者信息 |

---

## 4. 完整 Deployment 示例

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-with-gitaly-fuse
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
      # AppArmor 按需添加
      # annotations:
      #   container.apparmor.security.beta.kubernetes.io/gitaly-fuse: unconfined
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        # 主应用 - 无特权
        - name: main-app
          image: your-app:latest
          volumeMounts:
            - name: git-repo
              mountPath: /mnt/git
          env:
            - name: GITALY_FUSE_SIDECAR
              value: "http://localhost:9999"  # sidecar 挂载 API

        # Sidecar - FUSE 挂载
        - name: gitaly-fuse
          image: your-registry/gitaly-fuse:latest
          securityContext:
            capabilities:
              add:
                - SYS_ADMIN
          volumeMounts:
            - name: fuse-device
              mountPath: /dev/fuse
            - name: git-repo
              mountPath: /mnt/git
          env:
            - name: GITALY_ADDRESS
              value: "gitaly.gitlab.svc.cluster.local:8075"
            - name: GITALY_STORAGE
              value: "default"
            - name: GITALY_TOKEN
              valueFrom:
                secretKeyRef:
                  name: gitaly-credentials
                  key: token
          args:
            - "-mode=sidecar"
            - "-mount-api=:9999"
            - "/mnt/git"
          ports:
            - containerPort: 9999
              name: mount-api

      volumes:
        - name: fuse-device
          hostPath:
            path: /dev/fuse
            type: CharDevice
        - name: git-repo
          emptyDir: {}
```

---

## 5. 代码改造需求

当前 gitaly-fuse 为单次挂载、启动即挂载模式，需扩展以支持边车 + 动态挂载：

### 5.1 新增运行模式

| 模式 | 说明 |
|------|------|
| `default` | 当前行为：启动时挂载单一 repo |
| `sidecar` | 启动后监听挂载请求，按需挂载多个 repo |

### 5.2 挂载 API（sidecar 模式）

建议提供 HTTP API：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /mount | 挂载 repo，body: `{"repo": "...", "storage": "...", "branch": "..."}` |
| POST | /unmount | 卸载，body: `{"path": "/mnt/git/repo-id"}` |
| GET | /mounts | 列出已挂载的 repo |

### 5.3 多进程/多挂载

每个 repo 对应一次 `fuse.Mount()`，可考虑：

- **多进程**：每个挂载请求 fork 子进程运行 gitaly-fuse
- **或**：单进程多 goroutine，每个挂载一个独立 FUSE 连接（需验证 bazil/fuse 支持）

---

## 6. 注意事项汇总

### 6.1 安全

- 主容器不添加 SYS_ADMIN，仅 sidecar 需要
- GITALY_TOKEN 使用 Secret 注入，避免明文
- 挂载 API 若暴露，需鉴权或仅监听 localhost

### 6.2 网络

- Pod 需能访问 Gitaly gRPC 端口（通常 8075）
- 跨 Namespace 时使用完整 DNS：`gitaly.<namespace>.svc.cluster.local:8075`

### 6.3 生命周期

- sidecar 退出前应卸载所有 FUSE 挂载
- 主应用需处理 sidecar 重启导致的挂载暂时不可用

### 6.4 资源

- 每个 FUSE 挂载会占用一定内存与 Gitaly 连接
- 可对 sidecar 设置 `resources.limits` 避免 OOM

### 6.5 权限与写入

- 主容器对共享目录的写操作会经 FUSE 提交到 Gitaly
- 实际能否写入取决于 Gitaly/GitLab 权限配置
- 挂载点 umask 需保证主容器用户可写（若主容器非 root）

---

## 7. 实施路线图

| 阶段 | 内容 |
|------|------|
| **Phase 1** | 边车模式：单 repo、启动时挂载，验证主容器无特权可读写 |
| **Phase 2** | 增加挂载 API，支持按需挂载 |
| **Phase 3** | 多 repo 动态挂载，子目录命名与并发挂载 |
| **Phase 4** | 生产优化：健康检查、指标、优雅卸载 |

---

## 8. 参考

- [FUSE in Kubernetes](https://karlstoney.com/fuse-mount-in-kubernetes/)
- [Kubernetes Security Context](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/)
- 项目 README: [README.md](../README.md)
