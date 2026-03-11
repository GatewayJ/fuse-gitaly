// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package fuse 实现 Gitaly 仓库的 FUSE 文件系统挂载，支持 ls/cd/tree/mkdir/cat/vim/mv 等操作。
package fuse

import (
	"path"

	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/cache"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/config"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/gitalyclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// GitalyFS implements fs.FS and mounts a Gitaly repository as a filesystem.
type GitalyFS struct {
	Client *gitalyclient.Client
	Repo   *gitalypb.Repository
	Branch string
	User   *gitalypb.User
	Config config.Config
	Cache  *cache.Cache
}

var _ fs.FS = (*GitalyFS)(nil)

// Root returns the root node (repository tree root).
func (f *GitalyFS) Root() (fs.Node, error) {
	return &Dir{
		fs:    f,
		path:  "",
		inode: inodeHash(f.Branch, ""),
	}, nil
}

// InvalidatePaths 在写操作后使受影响路径的缓存失效。
// affectedPaths 为被修改的文件或目录路径（如 "a/b/file.txt" 或 "a/b"）。
func (f *GitalyFS) InvalidatePaths(affectedPaths ...string) {
	if f.Cache == nil {
		return
	}
	for _, p := range affectedPaths {
		if p == "" {
			p = "."
		}
		f.Cache.Invalidate(cache.KeyTree(f.Branch, p))
		f.Cache.Invalidate(cache.KeyMeta(f.Branch, p))
		f.Cache.Invalidate(cache.KeyBlobPath(f.Branch, p))
		for d := path.Dir(p); d != "." && d != ""; d = path.Dir(d) {
			f.Cache.Invalidate(cache.KeyTree(f.Branch, d))
		}
	}
	f.Cache.Invalidate(cache.KeyTree(f.Branch, "."))
}

// InvalidateBlob 使指定 OID 的 blob 缓存失效（用于文件更新后）。
func (f *GitalyFS) InvalidateBlob(oid string) {
	if f.Cache != nil && oid != "" {
		f.Cache.Invalidate(cache.KeyBlob(oid))
	}
}
