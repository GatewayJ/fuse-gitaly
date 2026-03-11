// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT

package fuse

import (
	"context"
	"sync"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/config"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/gitalyclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// FileHandle implements fs.Handle for file read/write.
type FileHandle struct {
	file  *File
	data  []byte
	dirty bool
	mu    sync.Mutex
}

var _ fs.Handle = (*FileHandle)(nil)
var _ fs.HandleReader = (*FileHandle)(nil)
var _ fs.HandleWriter = (*FileHandle)(nil)
var _ fs.HandleFlusher = (*FileHandle)(nil)
var _ fs.HandleReleaser = (*FileHandle)(nil)

func (h *FileHandle) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if int(req.Offset) >= len(h.data) {
		resp.Data = nil
		return nil
	}
	end := int(req.Offset) + req.Size
	if end > len(h.data) {
		end = len(h.data)
	}
	resp.Data = append([]byte(nil), h.data[req.Offset:end]...)
	return nil
}

func (h *FileHandle) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	end := int(req.Offset) + len(req.Data)
	if end > len(h.data) {
		newData := make([]byte, end)
		copy(newData, h.data)
		h.data = newData
	}
	copy(h.data[req.Offset:], req.Data)
	h.dirty = true
	resp.Size = len(req.Data)
	return nil
}

func (h *FileHandle) Flush(ctx context.Context, req *fuse.FlushRequest) error {
	return h.commitIfDirty(ctx)
}

func (h *FileHandle) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	return h.commitIfDirty(ctx)
}

func (h *FileHandle) commitIfDirty(ctx context.Context) error {
	h.mu.Lock()
	dirty := h.dirty
	data := append([]byte(nil), h.data...)
	oldOid := h.file.oid
	h.dirty = false
	h.mu.Unlock()
	if !dirty {
		return nil
	}
	ctx, cancel := config.WithTimeout(ctx, h.file.fs.Config.GRPCTimeout)
	defer cancel()
	_, err := h.file.fs.Client.UserCommitFiles(ctx, h.file.fs.Repo, h.file.fs.Branch, "FUSE edit: "+h.file.path, h.file.fs.User,
		gitalyclient.Action{
			Type:     gitalypb.UserCommitFilesActionHeader_UPDATE,
			FilePath: h.file.path,
			Content:  data,
		},
	)
	if err != nil {
		return err
	}
	h.file.fs.InvalidatePaths(h.file.path)
	h.file.fs.InvalidateBlob(oldOid)
	return nil
}
