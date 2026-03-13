// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package skillsclient 后台轮询：技能列表（发现新仓库）、last_commit（发现新 commit 时 pull）。
// 技能列表与 LastCommit 轮询运行在不同协程。
package skillsclient

import (
	"context"
	"log"
	"time"
)

// ListPollerConfig 技能列表轮询配置。
type ListPollerConfig struct {
	// Interval 轮询间隔。
	Interval time.Duration
}

// SkillsListPoller 后台轮询技能列表并刷新缓存，使新仓库能被 FUSE 发现。
type SkillsListPoller struct {
	client    *Client
	interval  time.Duration
	OnRefresh func([]Skill) // 拉取到新列表后调用，通常用于更新缓存（如按 official/user 写入 Cache）
}

// NewSkillsListPoller 创建技能列表轮询器。
func NewSkillsListPoller(client *Client, cfg ListPollerConfig) *SkillsListPoller {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	return &SkillsListPoller{client: client, interval: cfg.Interval}
}

// Start 在独立协程中启动轮询；ctx 取消时退出。
func (p *SkillsListPoller) Start(ctx context.Context) {
	go p.run(ctx)
}

func (p *SkillsListPoller) run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[skills] list poller stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *SkillsListPoller) poll(ctx context.Context) {
	skills, err := p.client.FetchSkills(ctx)
	if err != nil {
		log.Printf("[skills] list poller FetchSkills error: %v", err)
		return
	}
	if p.OnRefresh != nil {
		p.OnRefresh(skills)
	}
	log.Printf("[skills] list poller refreshed, skills count=%d", len(skills))
}

// PollerConfig LastCommit 轮询器配置。
type PollerConfig struct {
	// Interval 轮询间隔，默认 5 分钟。
	Interval time.Duration
	// Ref 查询 last_commit 使用的分支/tag，如 main。
	Ref string
}

// DefaultPollerConfig 返回默认轮询配置。
func DefaultPollerConfig() PollerConfig {
	return PollerConfig{
		Interval: 5 * time.Minute,
		Ref:      "main",
	}
}

// SkillsCommitPoller 后台轮询各技能的 last_commit，若与本地 HEAD 不一致则执行强制更新 (fetch+reset)。
type SkillsCommitPoller struct {
	client *Client
	cfg    PollerConfig
}

// NewSkillsCommitPoller 创建轮询器。
func NewSkillsCommitPoller(client *Client, cfg PollerConfig) *SkillsCommitPoller {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	return &SkillsCommitPoller{client: client, cfg: cfg}
}

// Start 在后台启动轮询；ctx 取消时退出。
func (p *SkillsCommitPoller) Start(ctx context.Context) {
	go p.run(ctx)
}

func (p *SkillsCommitPoller) run(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	// 启动后先执行一次
	p.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[skills] commit poller stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *SkillsCommitPoller) poll(ctx context.Context) {
	skills, err := p.client.FetchSkills(ctx)
	if err != nil {
		log.Printf("[skills] poller FetchSkills error: %v", err)
		return
	}
	updated := 0
	for _, skill := range skills {
		if skill.Name == "" || skill.Path == "" {
			continue
		}
		skillDir := p.client.SkillDir(skill)
		if !isGitDir(skillDir) {
			continue
		}
		commit, err := p.client.LastCommit(ctx, skill.Path, p.cfg.Ref)
		if err != nil {
			log.Printf("[skills] poller LastCommit skill=%s error: %v", skill.Name, err)
			continue
		}
		localHEAD, err := p.client.GetLocalHEAD(skillDir)
		if err != nil {
			log.Printf("[skills] poller GetLocalHEAD skill=%s error: %v", skill.Name, err)
			continue
		}
		if commit.ID == localHEAD {
			continue
		}
		log.Printf("[skills] poller skill=%s remote=%s local=%s, updating", skill.Name, commit.ID, localHEAD)
		if err := p.client.CloneOrPull(ctx, skill); err != nil {
			log.Printf("[skills] poller CloneOrPull skill=%s error: %v", skill.Name, err)
			continue
		}
		updated++
	}
	if updated > 0 {
		log.Printf("[skills] poller updated %d skill(s)", updated)
	}
}
