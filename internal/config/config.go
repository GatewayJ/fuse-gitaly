// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package config 提供 FUSE 挂载的运行时配置。
package config

import (
	"context"
	"time"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/cache"
)

// Config 挂载配置。
type Config struct {
	// Cache 缓存配置。
	Cache cache.Config
	// GRPCTimeout gRPC 调用超时，0 表示不限制。
	GRPCTimeout time.Duration
}

// Default 返回默认配置。
func Default() Config {
	return Config{
		Cache:       cache.DefaultConfig(),
		GRPCTimeout: 30 * time.Second,
	}
}

// WithTimeout 若配置了超时则包装 context。
// 返回的 cancel 必须调用（可用 defer cancel()）。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
