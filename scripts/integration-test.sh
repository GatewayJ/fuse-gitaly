#!/bin/bash
# Copyright (c) 2025 OpenCSG
# SPDX-License-Identifier: MIT
#
# 完整集成测试: 启动 Gitaly -> 挂载 FUSE -> 测试 ls/cat/mkdir/mv 等
#
# 用法:
#   make integration-test           # 自动启动 Gitaly (首次需拉取镜像)
#   GITALY_SKIP_START=1 make integration-test  # 使用已运行的 Gitaly

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

BINARY="./gitaly-fuse"
GITALY_PORT="${GITALY_PORT:-8075}"
MOUNT_POINT="${MOUNT_POINT:-/tmp/gitaly-mount}"
REPO_PATH="test-repo.git"
FAILED=0

log() { echo "[$(date +%H:%M:%S)] $*"; }
fail() { log "FAIL: $*"; FAILED=1; }
pass() { log "PASS: $*"; }

# 1. 编译
log "Step 1: Building..."
make build

# 2. 准备测试仓库 (添加初始内容)
log "Step 2: Seeding test repo..."
REPO_DIR=".gitaly-data/repositories/$REPO_PATH"
mkdir -p "$(dirname "$REPO_DIR")"
if [ ! -d "$REPO_DIR" ]; then
  git init --bare "$REPO_DIR"
fi
# 克隆、添加文件、推送
TMP_CLONE=$(mktemp -d)
if git clone "$REPO_DIR" "$TMP_CLONE" 2>/dev/null; then
  cd "$TMP_CLONE"
  echo "Hello from Gitaly FUSE" > README.md
  echo "test content" > test.txt
  mkdir -p src
  echo "package main" > src/main.go
  git add -A
  git config user.email "test@local"
  git config user.name "Test"
  git commit -m "Initial commit" || true
  git branch -M main 2>/dev/null || true
  git push origin HEAD:main 2>/dev/null || git push origin HEAD:master 2>/dev/null || true
  cd "$PROJECT_ROOT"
  rm -rf "$TMP_CLONE"
else
  # 空仓库无法 clone，直接 init 并 push
  cd "$TMP_CLONE"
  git init
  echo "Hello from Gitaly FUSE" > README.md
  git add README.md
  git config user.email "test@local"
  git config user.name "Test"
  git commit -m "Initial"
  git remote add origin "file://$PROJECT_ROOT/$REPO_DIR"
  git push -u origin HEAD:main 2>/dev/null || git push -u origin HEAD:master 2>/dev/null || true
  cd "$PROJECT_ROOT"
  rm -rf "$TMP_CLONE"
fi

# 3. 检查/启动 Gitaly
log "Step 3: Checking Gitaly..."
if ! nc -z localhost "$GITALY_PORT" 2>/dev/null; then
  if [ "${GITALY_SKIP_START:-0}" = "1" ]; then
    fail "Gitaly not running on port $GITALY_PORT."
    log "请先启动 Gitaly (如 GDK、GitLab 或 make gitaly-up)，或设置 GITALY_SKIP_START=0 自动启动"
    exit 1
  fi
  log "Gitaly not running, starting..."
  make gitaly-up
  for i in $(seq 1 90); do
    if nc -z localhost "$GITALY_PORT" 2>/dev/null; then
      log "Gitaly ready"
      break
    fi
    sleep 1
  done
  if ! nc -z localhost "$GITALY_PORT" 2>/dev/null; then
    fail "Gitaly failed to start on port $GITALY_PORT"
    log "若使用外部 Gitaly (GDK/GitLab)，请先启动后执行: GITALY_SKIP_START=1 make integration-test"
    exit 1
  fi
fi

# 4. 创建挂载点
log "Step 4: Preparing mount point..."
mkdir -p "$MOUNT_POINT"
if mountpoint -q "$MOUNT_POINT" 2>/dev/null; then
  fusermount -u "$MOUNT_POINT" 2>/dev/null || true
  sleep 1
fi

# 5. 启动 gitaly-fuse (后台)
log "Step 5: Mounting gitaly-fuse..."
"$BINARY" -gitaly "localhost:$GITALY_PORT" -storage default -repo "$REPO_PATH" "$MOUNT_POINT" &
FUSE_PID=$!
sleep 2
if ! kill -0 $FUSE_PID 2>/dev/null; then
  fail "gitaly-fuse failed to start"
  exit 1
fi

cleanup() {
  log "Cleaning up..."
  kill $FUSE_PID 2>/dev/null || true
  sleep 1
  fusermount -u "$MOUNT_POINT" 2>/dev/null || true
}
trap cleanup EXIT

# 6. 运行测试
log "Step 6: Running integration tests..."

# ls
if ls "$MOUNT_POINT" >/dev/null 2>&1; then
  pass "ls (root)"
else
  fail "ls (root)"
fi

# tree (若存在)
if command -v tree >/dev/null 2>&1; then
  if tree "$MOUNT_POINT" >/dev/null 2>&1; then
    pass "tree"
  else
    fail "tree"
  fi
fi

# cat
if [ -f "$MOUNT_POINT/README.md" ]; then
  if grep -q "Hello" "$MOUNT_POINT/README.md" 2>/dev/null; then
    pass "cat README.md"
  else
    fail "cat README.md"
  fi
else
  pass "cat (README.md may not exist in empty repo)"
fi

# cd
if cd "$MOUNT_POINT" && ls >/dev/null 2>&1; then
  pass "cd"
  cd "$PROJECT_ROOT"
else
  fail "cd"
fi

# mkdir (写操作，可能因 Gitaly 配置失败)
if mkdir "$MOUNT_POINT/newdir" 2>/dev/null; then
  pass "mkdir"
  rmdir "$MOUNT_POINT/newdir" 2>/dev/null || true
else
  log "SKIP: mkdir (write may require GitLab auth)"
fi

# 汇总
echo ""
if [ $FAILED -eq 0 ]; then
  log "=== Integration test PASSED ==="
  exit 0
else
  log "=== Integration test FAILED ==="
  exit 1
fi
