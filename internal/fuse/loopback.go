// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// loopback 实现基于本地目录的 FUSE 透传，用于挂载已 clone 的技能目录，支持 ls/cat/vim。

package fuse

import (
	"context"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// LoopbackFS 将本地目录挂载为 FUSE，支持读写（ls/cat/vim）。
type LoopbackFS struct {
	RootPath string
}

var _ fs.FS = (*LoopbackFS)(nil)

// Root 返回根目录节点。
func (f *LoopbackFS) Root() (fs.Node, error) {
	return &loopbackDir{path: f.RootPath, inode: loopbackInode(f.RootPath)}, nil
}

func loopbackInode(p string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(p))
	return h.Sum64()
}

// loopbackDir 表示本地目录。
type loopbackDir struct {
	path  string
	inode uint64
}

var _ fs.Node = (*loopbackDir)(nil)
var _ fs.NodeStringLookuper = (*loopbackDir)(nil)
var _ fs.HandleReadDirAller = (*loopbackDir)(nil)

func (d *loopbackDir) Attr(ctx context.Context, a *fuse.Attr) error {
	info, err := os.Stat(d.path)
	if err != nil {
		return err
	}
	a.Inode = d.inode
	if a.Inode == 0 {
		a.Inode = loopbackInode(d.path)
	}
	a.Mode = os.ModeDir | 0o755
	a.Size = 0
	a.Mtime = info.ModTime()
	a.Ctime = info.ModTime()
	a.Atime = info.ModTime()
	return nil
}

func (d *loopbackDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if name == "." || name == ".." {
		return nil, syscall.ENOENT
	}
	childPath := filepath.Join(d.path, name)
	info, err := os.Stat(childPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, syscall.ENOENT
		}
		return nil, err
	}
	inode := loopbackInode(childPath)
	if info.IsDir() {
		return &loopbackDir{path: childPath, inode: inode}, nil
	}
	return &loopbackFile{path: childPath, inode: inode}, nil
}

func (d *loopbackDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return nil, err
	}
	var out []fuse.Dirent
	for _, e := range entries {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		childPath := filepath.Join(d.path, name)
		ent := fuse.Dirent{
			Name:  name,
			Inode: loopbackInode(childPath),
		}
		if e.IsDir() {
			ent.Type = fuse.DT_Dir
		} else {
			ent.Type = fuse.DT_File
		}
		out = append(out, ent)
	}
	return out, nil
}

// loopbackFile 表示本地文件。
type loopbackFile struct {
	path  string
	inode uint64
}

var _ fs.Node = (*loopbackFile)(nil)
var _ fs.NodeOpener = (*loopbackFile)(nil)

func (f *loopbackFile) Attr(ctx context.Context, a *fuse.Attr) error {
	info, err := os.Stat(f.path)
	if err != nil {
		return err
	}
	a.Inode = f.inode
	if a.Inode == 0 {
		a.Inode = loopbackInode(f.path)
	}
	a.Mode = 0o644
	a.Size = uint64(info.Size())
	a.Mtime = info.ModTime()
	a.Ctime = info.ModTime()
	a.Atime = info.ModTime()
	return nil
}

func (f *loopbackFile) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	log.Printf("[skills] loopback read file=%s (no API/git request, from local clone)", f.path)
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, syscall.ENOENT
		}
		return nil, err
	}
	resp.Flags |= fuse.OpenKeepCache
	return &loopbackFileHandle{path: f.path, data: data, dirty: false}, nil
}

// loopbackFileHandle 支持读和写，Flush/Release 时写回磁盘。
type loopbackFileHandle struct {
	path  string
	data  []byte
	dirty bool
	mu    sync.Mutex
}

var _ fs.Handle = (*loopbackFileHandle)(nil)
var _ fs.HandleReader = (*loopbackFileHandle)(nil)
var _ fs.HandleWriter = (*loopbackFileHandle)(nil)
var _ fs.HandleFlusher = (*loopbackFileHandle)(nil)
var _ fs.HandleReleaser = (*loopbackFileHandle)(nil)

func (h *loopbackFileHandle) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
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

func (h *loopbackFileHandle) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
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

func (h *loopbackFileHandle) Flush(ctx context.Context, req *fuse.FlushRequest) error {
	return h.writeBack()
}

func (h *loopbackFileHandle) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	return h.writeBack()
}

func (h *loopbackFileHandle) writeBack() error {
	h.mu.Lock()
	dirty := h.dirty
	data := append([]byte(nil), h.data...)
	h.dirty = false
	h.mu.Unlock()
	if !dirty {
		return nil
	}
	return os.WriteFile(h.path, data, 0o644)
}
