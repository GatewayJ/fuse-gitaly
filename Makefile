# Copyright (c) 2025 OpenCSG
# SPDX-License-Identifier: MIT
#
# opencsg-fuse-gitaly Makefile
# 使用: make help 查看所有目标

BINARY := gitaly-fuse
MAIN_PKG := ./cmd/gitaly-fuse
GITALY_PORT ?= 8075
MOUNT_POINT ?= /tmp/gitaly-mount

.PHONY: fmt test build clean gitaly-up gitaly-down gitaly-logs run-info integration-test help

# 代码格式化
fmt:
	go fmt ./...

# 运行测试
test:
	go test -v ./...

# 编译
build:
	go build -o $(BINARY) $(MAIN_PKG)

# 清理
clean:
	rm -f $(BINARY)
	rm -rf .gitaly-data

# 启动本地 Gitaly (Docker)
gitaly-up:
	@mkdir -p .gitaly-data/repositories
	@if [ ! -f config/gitaly.toml ]; then echo "Error: config/gitaly.toml not found"; exit 1; fi
	@if [ ! -d .gitaly-data/repositories/test-repo.git ]; then \
		git init --bare .gitaly-data/repositories/test-repo.git; \
		echo "Initialized test repo at .gitaly-data/repositories/test-repo.git"; \
	fi
	docker compose up -d gitaly
	@echo "Waiting for Gitaly to start..."
	@sleep 3
	@echo "Gitaly is running at localhost:$(GITALY_PORT)"

# 停止 Gitaly
gitaly-down:
	docker compose down

# 查看 Gitaly 日志
gitaly-logs:
	docker compose logs -f gitaly

# 完整集成测试 (需 Gitaly 运行 + FUSE 支持)
integration-test:
	@chmod +x scripts/integration-test.sh
	@./scripts/integration-test.sh

# 展示运行所需信息
run-info:
	@echo "=========================================="
	@echo "  gitaly-fuse 运行信息"
	@echo "=========================================="
	@echo ""
	@echo "1. 前置条件: 确保 Gitaly 已启动"
	@echo "   make gitaly-up    # 启动本地 Gitaly"
	@echo ""
	@echo "2. 创建挂载点:"
	@echo "   mkdir -p $(MOUNT_POINT)"
	@echo ""
	@echo "3. 运行命令 (本地 Gitaly):"
	@echo "   ./$(BINARY) -gitaly localhost:$(GITALY_PORT) -storage default -repo test-repo.git $(MOUNT_POINT)"
	@echo ""
	@echo "4. 或使用环境变量:"
	@echo "   export GITALY_ADDRESS=localhost:$(GITALY_PORT)"
	@echo "   export GITALY_REPO=test-repo.git"
	@echo "   ./$(BINARY) $(MOUNT_POINT)"
	@echo ""
	@echo "5. 卸载:"
	@echo "   fusermount -u $(MOUNT_POINT)"
	@echo "   或 Ctrl+C"
	@echo ""

# 默认目标
.DEFAULT_GOAL := help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  fmt              - 格式化代码"
	@echo "  test             - 运行测试"
	@echo "  build            - 编译二进制"
	@echo "  clean            - 清理构建产物"
	@echo "  gitaly-up        - 启动本地 Gitaly (Docker)"
	@echo "  gitaly-down      - 停止 Gitaly"
	@echo "  gitaly-logs      - 查看 Gitaly 日志"
	@echo "  integration-test - 完整集成测试 (Gitaly+FUSE)"
	@echo "  run-info         - 展示运行所需信息"
	@echo "  help             - 显示此帮助"
