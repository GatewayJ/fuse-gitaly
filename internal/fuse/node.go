// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT

package fuse

import (
	"context"
	"hash/fnv"
	"os"
	"path"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/cache"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/config"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/gitalyclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func inodeHash(revision, p string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(revision))
	h.Write([]byte{0})
	h.Write([]byte(p))
	return h.Sum64()
}

// Dir implements a directory node backed by Gitaly tree.
type Dir struct {
	fs    *GitalyFS
	path  string // path within repo (empty = root)
	inode uint64
}

var _ fs.Node = (*Dir)(nil)
var _ fs.NodeStringLookuper = (*Dir)(nil)
var _ fs.HandleReadDirAller = (*Dir)(nil)
var _ fs.NodeMkdirer = (*Dir)(nil)
var _ fs.NodeCreater = (*Dir)(nil)
var _ fs.NodeRenamer = (*Dir)(nil)

func (d *Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = d.inode
	if a.Inode == 0 {
		a.Inode = inodeHash(d.fs.Branch, d.path)
	}
	a.Mode = os.ModeDir | 0o555
	a.Size = 0
	return nil
}

func (d *Dir) getTreeEntries(ctx context.Context) ([]*gitalypb.TreeEntry, error) {
	ctx, cancel := config.WithTimeout(ctx, d.fs.Config.GRPCTimeout)
	defer cancel()
	pathKey := d.path
	if pathKey == "" {
		pathKey = "."
	}
	if d.fs.Cache != nil {
		if v, ok := d.fs.Cache.Get(cache.KeyTree(d.fs.Branch, pathKey)); ok {
			return v.([]*gitalypb.TreeEntry), nil
		}
	}
	entries, err := d.fs.Client.GetTreeEntries(ctx, d.fs.Repo, d.fs.Branch, d.path, false)
	if err != nil {
		return nil, err
	}
	if d.fs.Cache != nil {
		d.fs.Cache.Set(cache.KeyTree(d.fs.Branch, pathKey), entries)
	}
	return entries, nil
}

func (d *Dir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if name == "." || name == ".." {
		return nil, syscall.ENOENT
	}
	entries, err := d.getTreeEntries(ctx)
	if err != nil {
		return nil, err
	}
	childPath := gitalyclient.JoinPath(d.path, name)
	for _, e := range entries {
		entryName := string(e.GetPath())
		if path.Base(entryName) == name || entryName == name {
			childPath = entryName
			if e.GetType() == gitalypb.TreeEntry_TREE {
				return &Dir{
					fs:    d.fs,
					path:  childPath,
					inode: inodeHash(d.fs.Branch, childPath),
				}, nil
			}
			if e.GetType() == gitalypb.TreeEntry_BLOB {
				return &File{
					fs:    d.fs,
					path:  childPath,
					oid:   e.GetOid(),
					inode: inodeHash(d.fs.Branch, childPath),
				}, nil
			}
			return nil, syscall.ENOENT
		}
	}
	return nil, syscall.ENOENT
}

func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	entries, err := d.getTreeEntries(ctx)
	if err != nil {
		return nil, err
	}
	var out []fuse.Dirent
	for _, e := range entries {
		entryPath := string(e.GetPath())
		parent := path.Dir(entryPath)
		if d.path != "" && parent != d.path {
			continue
		}
		if d.path == "" && parent != "." {
			continue
		}
		base := path.Base(entryPath)
		if base == "." || base == ".." {
			continue
		}
		ent := fuse.Dirent{
			Name:  base,
			Inode: inodeHash(d.fs.Branch, entryPath),
		}
		switch e.GetType() {
		case gitalypb.TreeEntry_TREE, gitalypb.TreeEntry_COMMIT:
			ent.Type = fuse.DT_Dir
		default:
			ent.Type = fuse.DT_File
		}
		out = append(out, ent)
	}
	return out, nil
}

func (d *Dir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	newPath := gitalyclient.JoinPath(d.path, req.Name)
	ctx, cancel := config.WithTimeout(ctx, d.fs.Config.GRPCTimeout)
	defer cancel()
	_, err := d.fs.Client.UserCommitFiles(ctx, d.fs.Repo, d.fs.Branch, "FUSE mkdir: "+newPath, d.fs.User,
		gitalyclient.Action{
			Type:     gitalypb.UserCommitFilesActionHeader_CREATE_DIR,
			FilePath: newPath,
		},
	)
	if err != nil {
		return nil, err
	}
	d.fs.InvalidatePaths(newPath, d.path)
	return &Dir{
		fs:    d.fs,
		path:  newPath,
		inode: inodeHash(d.fs.Branch, newPath),
	}, nil
}

func (d *Dir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	newPath := gitalyclient.JoinPath(d.path, req.Name)
	content := []byte{}
	ctx, cancel := config.WithTimeout(ctx, d.fs.Config.GRPCTimeout)
	defer cancel()
	if req.Mode&os.ModeDir != 0 {
		_, err := d.fs.Client.UserCommitFiles(ctx, d.fs.Repo, d.fs.Branch, "FUSE mkdir: "+newPath, d.fs.User,
			gitalyclient.Action{
				Type:     gitalypb.UserCommitFilesActionHeader_CREATE_DIR,
				FilePath: newPath,
			},
		)
		if err != nil {
			return nil, nil, err
		}
		d.fs.InvalidatePaths(newPath, d.path)
		dir := &Dir{
			fs:    d.fs,
			path:  newPath,
			inode: inodeHash(d.fs.Branch, newPath),
		}
		return dir, dir, nil
	}
	_, err := d.fs.Client.UserCommitFiles(ctx, d.fs.Repo, d.fs.Branch, "FUSE create: "+newPath, d.fs.User,
		gitalyclient.Action{
			Type:     gitalypb.UserCommitFilesActionHeader_CREATE,
			FilePath: newPath,
			Content:  content,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	d.fs.InvalidatePaths(newPath, d.path)
	f := &File{
		fs:    d.fs,
		path:  newPath,
		oid:   "",
		inode: inodeHash(d.fs.Branch, newPath),
	}
	h := &FileHandle{file: f, data: content, dirty: false}
	return f, h, nil
}

func (d *Dir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	prevPath := gitalyclient.JoinPath(d.path, req.OldName)
	var targetPath string
	if nd, ok := newDir.(*Dir); ok {
		targetPath = gitalyclient.JoinPath(nd.path, req.NewName)
	} else {
		return syscall.EIO
	}
	ctx, cancel := config.WithTimeout(ctx, d.fs.Config.GRPCTimeout)
	defer cancel()
	_, err := d.fs.Client.UserCommitFiles(ctx, d.fs.Repo, d.fs.Branch, "FUSE mv: "+prevPath+" -> "+targetPath, d.fs.User,
		gitalyclient.Action{
			Type:         gitalypb.UserCommitFilesActionHeader_MOVE,
			FilePath:     targetPath,
			PreviousPath: prevPath,
		},
	)
	if err != nil {
		return err
	}
	d.fs.InvalidatePaths(prevPath, targetPath)
	return nil
}

// File implements a file node backed by Gitaly blob.
type File struct {
	fs    *GitalyFS
	path  string
	oid   string
	inode uint64
}

var _ fs.Node = (*File)(nil)
var _ fs.NodeOpener = (*File)(nil)

func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = f.inode
	if a.Inode == 0 {
		a.Inode = inodeHash(f.fs.Branch, f.path)
	}
	a.Mode = 0o444
	if f.oid != "" {
		pathKey := f.path
		if pathKey == "" {
			pathKey = "."
		}
		if f.fs.Cache != nil {
			if v, ok := f.fs.Cache.Get(cache.KeyMeta(f.fs.Branch, pathKey)); ok {
				a.Size = uint64(v.(int64))
				return nil
			}
		}
		ctx, cancel := config.WithTimeout(ctx, f.fs.Config.GRPCTimeout)
		defer cancel()
		_, _, size, _, _, err := f.fs.Client.GetTreeEntry(ctx, f.fs.Repo, f.fs.Branch, f.path, 1)
		if err == nil {
			a.Size = uint64(size)
			if f.fs.Cache != nil {
				f.fs.Cache.Set(cache.KeyMeta(f.fs.Branch, pathKey), size)
			}
		}
	}
	return nil
}

func (f *File) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	ctx, cancel := config.WithTimeout(ctx, f.fs.Config.GRPCTimeout)
	defer cancel()
	pathKey := f.path
	if pathKey == "" {
		pathKey = "."
	}
	var data []byte
	var err error
	if f.oid != "" {
		if f.fs.Cache != nil {
			if v, ok := f.fs.Cache.Get(cache.KeyBlob(f.oid)); ok {
				data = v.([]byte)
			}
		}
		if data == nil {
			data, err = f.fs.Client.GetBlob(ctx, f.fs.Repo, f.oid, -1)
			if err != nil {
				return nil, err
			}
			if f.fs.Cache != nil && (f.fs.Config.Cache.MaxBlobSize <= 0 || int64(len(data)) <= f.fs.Config.Cache.MaxBlobSize) {
				f.fs.Cache.Set(cache.KeyBlob(f.oid), data)
			}
		}
	} else {
		if f.fs.Cache != nil {
			if v, ok := f.fs.Cache.Get(cache.KeyBlobPath(f.fs.Branch, pathKey)); ok {
				data = v.([]byte)
			}
		}
		if data == nil {
			_, _, _, _, blobData, err := f.fs.Client.GetTreeEntry(ctx, f.fs.Repo, f.fs.Branch, f.path, -1)
			if err == nil {
				data = blobData
				if f.fs.Cache != nil && (f.fs.Config.Cache.MaxBlobSize <= 0 || int64(len(data)) <= f.fs.Config.Cache.MaxBlobSize) {
					f.fs.Cache.Set(cache.KeyBlobPath(f.fs.Branch, pathKey), data)
				}
			}
		}
	}
	resp.Flags |= fuse.OpenKeepCache
	return &FileHandle{file: f, data: data, dirty: false}, nil
}
