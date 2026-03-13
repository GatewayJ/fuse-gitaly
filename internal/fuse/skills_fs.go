// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// skills_fs 实现基于技能 API 与本地 clone 目录的 FUSE，支持 ls/cat/vim 与列表缓存。

package fuse

import (
	"context"
	"log"
	"os"
	"strings"
	"syscall"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/cache"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/skillsclient"
)

// SkillsFS 挂载技能目录：根目录为 official/ 与 user/，其下为各技能名，对应 clone 到本地的仓库目录。
type SkillsFS struct {
	Client *skillsclient.Client
	Cache  *cache.Cache
}

var _ fs.FS = (*SkillsFS)(nil)

// Root 返回根节点，其下为 "official" 和 "user" 两个目录。
func (f *SkillsFS) Root() (fs.Node, error) {
	return &skillsRootDir{fs: f}, nil
}

type skillsRootDir struct {
	fs *SkillsFS
}

var _ fs.Node = (*skillsRootDir)(nil)
var _ fs.NodeStringLookuper = (*skillsRootDir)(nil)
var _ fs.HandleReadDirAller = (*skillsRootDir)(nil)

func (d *skillsRootDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 1
	a.Mode = os.ModeDir | 0o555
	a.Size = 0
	return nil
}

func (d *skillsRootDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	switch name {
	case "official":
		return &skillsCategoryDir{fs: d.fs, category: "official"}, nil
	case "user":
		return &skillsCategoryDir{fs: d.fs, category: "user"}, nil
	default:
		return nil, syscall.ENOENT
	}
}

var skillsRootEntries = []fuse.Dirent{
	{Inode: 2, Name: "official", Type: fuse.DT_Dir},
	{Inode: 3, Name: "user", Type: fuse.DT_Dir},
}

func (d *skillsRootDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return skillsRootEntries, nil
}

// skillsCategoryDir 表示 official 或 user 目录，其子节点为技能名，来自 API 并缓存。
type skillsCategoryDir struct {
	fs       *SkillsFS
	category string // "official" 或 "user"
}

var _ fs.Node = (*skillsCategoryDir)(nil)
var _ fs.NodeStringLookuper = (*skillsCategoryDir)(nil)
var _ fs.HandleReadDirAller = (*skillsCategoryDir)(nil)

func (d *skillsCategoryDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = inodeHash(d.category, "")
	if a.Inode == 0 {
		a.Inode = 2
		if d.category == "user" {
			a.Inode = 3
		}
	}
	a.Mode = os.ModeDir | 0o555
	a.Size = 0
	return nil
}

func (d *skillsCategoryDir) getSkills(ctx context.Context) ([]skillsclient.Skill, error) {
	cacheKey := cache.KeySkillsList(d.category)
	if d.fs.Cache != nil {
		if v, ok := d.fs.Cache.Get(cacheKey); ok {
			list := v.([]skillsclient.Skill)
			log.Printf("[skills] getSkills category=%s cache_hit=true count=%d (no API request)", d.category, len(list))
			return list, nil
		}
	}
	skills, err := d.fs.Client.FetchSkills(ctx)
	if err != nil {
		return nil, err
	}
	var list []skillsclient.Skill
	for _, s := range skills {
		if d.category == "official" && s.BuiltIn {
			list = append(list, s)
		}
		if d.category == "user" && !s.BuiltIn {
			list = append(list, s)
		}
	}
	if d.fs.Cache != nil {
		d.fs.Cache.Set(cacheKey, list)
	}
	return list, nil
}

func (d *skillsCategoryDir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	if name == "" || strings.Contains(name, "/") {
		return nil, syscall.ENOENT
	}
	skills, err := d.getSkills(ctx)
	if err != nil {
		return nil, err
	}
	var found *skillsclient.Skill
	for i := range skills {
		if skills[i].Name == name {
			found = &skills[i]
			break
		}
	}
	if found == nil {
		return nil, syscall.ENOENT
	}
	// 确保已 clone 并强制与远端一致，然后返回指向本地目录的 loopback 根节点
	if err := d.fs.Client.CloneOrPull(ctx, *found); err != nil {
		return nil, err
	}
	skillPath := d.fs.Client.SkillDir(*found)
	return &loopbackDir{path: skillPath, inode: inodeHash(d.category, name)}, nil
}

func (d *skillsCategoryDir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	skills, err := d.getSkills(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]fuse.Dirent, 0, len(skills))
	for _, s := range skills {
		ent := fuse.Dirent{
			Name:  s.Name,
			Inode: inodeHash(d.category, s.Name),
			Type:  fuse.DT_Dir,
		}
		out = append(out, ent)
	}
	return out, nil
}
