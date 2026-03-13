// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package skillsclient 对接 AgentHub/CSGHub 技能 API：拉取技能列表并 clone 到本地工作目录。
package skillsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWorkspaceDir = "/root/.agentichub"
	SuccessMarkerFile   = ".skills_initialized"
	FailureMarkerFile   = ".skills_initialization_failed"
)

// Skill 表示从 API 返回的一条技能。
type Skill struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
	BuiltIn     bool   `json:"built_in"`
}

// Config 为技能 API 与本地同步的配置。
type Config struct {
	BaseURL      string        // 例如 https://api.example.com
	Token        string        // Bearer token
	UserName     string        // 用于 clone URL 认证
	WorkspaceDir string        // 工作目录，技能将 clone 到 workspace/skills/official 或 user
	HTTPTimeout  time.Duration // 请求超时，默认 60s
	Ref         string        // 分支名，用于 fetch/reset，如 main，空则用 "main"
}

// Client 用于拉取技能列表并执行 git clone 或强制更新 (fetch+reset)。
type Client struct {
	cfg    Config
	client *http.Client
}

// NewClient 创建技能 API 客户端。
func NewClient(cfg Config) *Client {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 60 * time.Second
	}
	if cfg.WorkspaceDir == "" {
		cfg.WorkspaceDir = DefaultWorkspaceDir
	}
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	}
}

// apiSkillsResponse 与 GET /api/v1/agent/skills 的响应结构一致。
type apiSkillsResponse struct {
	Data []Skill `json:"data"`
}

// FetchSkills 调用 GET {base_url}/api/v1/agent/skills 获取技能列表。
func (c *Client) FetchSkills(ctx context.Context) ([]Skill, error) {
	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/api/v1/agent/skills"
	log.Printf("[skills] FetchSkills url=%s", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("[skills] FetchSkills new_request error: %v", err)
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[skills] FetchSkills request error: %v", err)
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		log.Printf("[skills] FetchSkills HTTP error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(buf.String()))
		return nil, fmt.Errorf("fetch skills: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}

	var out apiSkillsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Printf("[skills] FetchSkills decode error: %v", err)
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Data == nil {
		out.Data = []Skill{}
	}
	log.Printf("[skills] FetchSkills ok count=%d", len(out.Data))
	return out.Data, nil
}

// SkillsDir 返回 skills 根目录（workspace/skills）。
func (c *Client) SkillsDir() string {
	return filepath.Join(c.cfg.WorkspaceDir, "skills")
}

// CloneURL 返回该技能的 git clone URL（含认证占位，用于日志脱敏）。
func (c *Client) CloneURL(skill Skill, redactToken bool) string {
	base := strings.TrimPrefix(c.cfg.BaseURL, "https://")
	base = strings.TrimPrefix(base, "http://")
	tok := c.cfg.Token
	if redactToken && tok != "" {
		tok = "***"
	}
	return fmt.Sprintf("https://%s:%s@%s/skills/%s.git", c.cfg.UserName, tok, base, skill.Path)
}

// CloneOrPull 将技能 clone 到 workspace/skills/official 或 user/<name>；若已存在则强制与远端一致 (fetch + reset --hard)。
func (c *Client) CloneOrPull(ctx context.Context, skill Skill) error {
	if skill.Name == "" || skill.Path == "" {
		log.Printf("[skills] CloneOrPull skip: name or path empty")
		return fmt.Errorf("skill name or path empty")
	}
	subDir := "official"
	if !skill.BuiltIn {
		subDir = "user"
	}
	parent := filepath.Join(c.cfg.WorkspaceDir, "skills", subDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	skillDir := filepath.Join(parent, skill.Name)
	cloneURL := c.CloneURL(skill, false)

	if isGitDir(skillDir) {
		ref := c.cfg.Ref
		if ref == "" {
			ref = "main"
		}
		log.Printf("[skills] CloneOrPull name=%s dir=%s action=force_update (fetch+reset)", skill.Name, subDir)
		// 强制与远端一致：fetch 后 reset --hard，忽略本地暂存/未提交/偏离
		fetch := exec.CommandContext(ctx, "git", "fetch", "origin")
		fetch.Dir = skillDir
		if out, err := fetch.CombinedOutput(); err != nil {
			log.Printf("[skills] CloneOrPull name=%s git fetch error: %v output=%s", skill.Name, err, bytes.TrimSpace(out))
			return fmt.Errorf("git fetch: %w: %s", err, bytes.TrimSpace(out))
		}
		reset := exec.CommandContext(ctx, "git", "reset", "--hard", "origin/"+ref)
		reset.Dir = skillDir
		out, err := reset.CombinedOutput()
		if err != nil {
			log.Printf("[skills] CloneOrPull name=%s git reset --hard error: %v output=%s", skill.Name, err, bytes.TrimSpace(out))
			return fmt.Errorf("git reset --hard origin/%s: %w: %s", ref, err, bytes.TrimSpace(out))
		}
		log.Printf("[skills] CloneOrPull name=%s force_update ok", skill.Name)
	} else {
		log.Printf("[skills] CloneOrPull name=%s dir=%s action=git_clone path=%s", skill.Name, subDir, skill.Path)
		if err := os.RemoveAll(skillDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove existing dir: %w", err)
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--single-branch", "--no-tags", cloneURL, skillDir)
		cmd.Dir = parent
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[skills] CloneOrPull name=%s git_clone error: %v output=%s", skill.Name, err, bytes.TrimSpace(out))
			return fmt.Errorf("git clone: %w: %s", err, bytes.TrimSpace(out))
		}
		log.Printf("[skills] CloneOrPull name=%s git_clone ok", skill.Name)
	}

	if skill.Description != "" {
		descPath := filepath.Join(skillDir, ".description")
		if err := os.WriteFile(descPath, []byte(skill.Description), 0o644); err != nil {
			log.Printf("[skills] CloneOrPull name=%s write .description error: %v", skill.Name, err)
			return fmt.Errorf("write .description: %w", err)
		}
	}
	return nil
}

func isGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// Commit 与 GET /api/v1/skills/:namespace/:name/last_commit 响应结构一致。
type Commit struct {
	ID             string `json:"id"`
	CommitterName  string `json:"committer_name"`
	CommitterEmail string `json:"committer_email"`
	CommitterDate  string `json:"committer_date"`
	Message        string `json:"message"`
	AuthorName     string `json:"author_name"`
	AuthorEmail    string `json:"author_email"`
	AuthoredDate   string `json:"authored_date"`
	CreatedAt      string `json:"created_at"`
}

// LastCommit 调用 GET /api/v1/skills/:namespace/:name/last_commit?ref=xxx，返回该仓库在 ref 上的最新 commit。
// path 格式为 "namespace/name"（与 Skill.Path 一致），ref 为分支或 tag，如 main。
func (c *Client) LastCommit(ctx context.Context, pathParam, ref string) (*Commit, error) {
	parts := strings.SplitN(pathParam, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("path must be namespace/name, got %q", pathParam)
	}
	namespace, name := parts[0], parts[1]
	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/api/v1/skills/" + namespace + "/" + name + "/last_commit?ref=" + ref
	log.Printf("[skills] LastCommit path=%s ref=%s url=%s", pathParam, ref, url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[skills] LastCommit request error: %v", err)
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		log.Printf("[skills] LastCommit HTTP error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(buf.String()))
		return nil, fmt.Errorf("last_commit: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}

	// 服务端返回标准包装 {"msg":"OK","data":{...commit...}}，需解码 data 字段
	var envelope struct {
		Data Commit `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode commit: %w", err)
	}
	commit := &envelope.Data
	log.Printf("[skills] LastCommit ok path=%s commit_id=%s", pathParam, commit.ID)
	return commit, nil
}

// GetLocalHEAD 返回本地技能目录当前 HEAD 的 commit id（git rev-parse HEAD）。
func (c *Client) GetLocalHEAD(skillDir string) (string, error) {
	if !isGitDir(skillDir) {
		return "", fmt.Errorf("not a git dir: %s", skillDir)
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = skillDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SkillDir 返回某技能在 workspace 下的本地目录路径（无论是否存在）。
func (c *Client) SkillDir(skill Skill) string {
	subDir := "official"
	if !skill.BuiltIn {
		subDir = "user"
	}
	return filepath.Join(c.cfg.WorkspaceDir, "skills", subDir, skill.Name)
}

// SyncAll 拉取技能列表并并发 clone/pull 所有技能；maxConcurrent 为最大并发数。
func (c *Client) SyncAll(ctx context.Context, maxConcurrent int) (succeeded, failed int, err error) {
	log.Printf("[skills] SyncAll start max_concurrent=%d", maxConcurrent)
	skills, err := c.FetchSkills(ctx)
	if err != nil {
		log.Printf("[skills] SyncAll FetchSkills error: %v", err)
		return 0, 0, err
	}
	if len(skills) == 0 {
		log.Printf("[skills] SyncAll no skills to sync")
		return 0, 0, nil
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 20
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	var okCount, failCount int
	var mu sync.Mutex

	for i := range skills {
		s := skills[i]
		if s.Name == "" || s.Path == "" {
			continue
		}
		wg.Add(1)
		go func(skill Skill) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e := c.CloneOrPull(ctx, skill)
			mu.Lock()
			if e != nil {
				failCount++
			} else {
				okCount++
			}
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	log.Printf("[skills] SyncAll done succeeded=%d failed=%d", okCount, failCount)
	return okCount, failCount, nil
}
