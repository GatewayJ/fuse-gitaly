// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package fuse 实现 Gitaly 仓库的 FUSE 文件系统挂载，支持 ls/cd/tree/mkdir/cat/vim/mv 等操作。
package fuse

import (
	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/gitalyclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// GitalyFS implements fs.FS and mounts a Gitaly repository as a filesystem.
type GitalyFS struct {
	Client *gitalyclient.Client
	Repo   *gitalypb.Repository
	Branch string
	User   *gitalypb.User
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
