// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// gitaly-fuse 支持两种挂载模式：Gitaly 仓库 或 技能 API (Skills)，均支持 ls/cd/tree/cat/vim 与缓存。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	bazilfuse "bazil.org/fuse"
	"bazil.org/fuse/fs"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/cache"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/config"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/fuse"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/gitalyclient"
	"github.com/opcsg/opencsg-fuse-gitaly/internal/skillsclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func main() {
	os.Exit(run())
}

func run() int {
	mode := flag.String("mode", getEnv("FUSE_MODE", "gitaly"), "挂载模式: gitaly | skills")

	// Gitaly 专用
	gitalyAddr := flag.String("gitaly", getEnv("GITALY_ADDRESS", ""), "[gitaly] Gitaly 地址 (如 localhost:8075)")
	storage := flag.String("storage", getEnv("GITALY_STORAGE", "default"), "[gitaly] 存储名称")
	repoPath := flag.String("repo", getEnv("GITALY_REPO", ""), "[gitaly] 仓库相对路径 (如 test-repo.git)")
	branch := flag.String("branch", getEnv("GITALY_BRANCH", ""), "[gitaly] 分支，空则使用默认分支")
	userName := flag.String("user", getEnv("GITALY_USER", "fuse"), "[gitaly] 提交作者名")
	userEmail := flag.String("email", getEnv("GITALY_EMAIL", "fuse@local"), "[gitaly] 提交作者邮箱")
	gitalyToken := flag.String("token", getEnv("GITALY_TOKEN", ""), "[gitaly] Bearer token (gRPC 认证)")
	grpcTimeout := flag.Duration("grpc-timeout", getEnvDuration("GITALY_GRPC_TIMEOUT", 30*time.Second), "[gitaly] gRPC 超时")

	// Skills 专用
	skillsBaseURL := flag.String("base-url", getEnv("CSGHUB_API_BASE_URL", ""), "[skills] API 根地址")
	skillsToken := flag.String("skills-token", getEnv("CSGHUB_USER_TOKEN", ""), "[skills] Bearer token")
	skillsUser := flag.String("skills-user", getEnv("CSGHUB_USER_NAME", ""), "[skills] 用户名")
	workspaceDir := flag.String("workspace", getEnv("AGENTICHUB_WORKSPACE", skillsclient.DefaultWorkspaceDir), "[skills] 工作目录")
	skillsRef := flag.String("skills-ref", getEnv("CSGHUB_SKILLS_REF", "main"), "[skills] last_commit 与强制更新使用的 ref（分支/tag，如 main）")
	skillsPollInterval := flag.Duration("skills-poll-interval", 5*time.Minute, "[skills] 轮询 last_commit 间隔，支持 30s/5m/1h 等，0=不轮询")
	skillsListPollInterval := flag.Duration("skills-list-poll-interval", 5*time.Minute, "[skills] 轮询技能列表间隔（发现新仓库），支持 30s/5m/1h 等，0=不轮询")

	// 通用缓存（gitaly 用于 tree/blob，skills 用于技能列表）
	cacheEnabled := flag.Bool("cache", getEnvBool("GITALY_CACHE", true), "是否启用缓存")
	cacheMaxEntries := flag.Int("cache-max-entries", getEnvInt("GITALY_CACHE_MAX_ENTRIES", 1000), "缓存最大条目数")
	cacheTTL := flag.Duration("cache-ttl", getEnvDuration("GITALY_CACHE_TTL", 5*time.Minute), "缓存 TTL")
	cacheMaxBlobSize := flag.Int64("cache-max-blob-size", getEnvInt64("GITALY_CACHE_MAX_BLOB_SIZE", 1<<20), "[gitaly] 超过此大小的 blob 不缓存，0=全部缓存")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <mount_point>\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Gitaly:  %s -mode=gitaly -gitaly localhost:8075 -repo test-repo.git /mnt/git\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Skills: %s -mode=skills -base-url https://api.example.com -skills-token TOKEN /mnt/skills\n", os.Args[0])
	}
	flag.Parse()

	mountPoint := ""
	if flag.NArg() > 0 {
		mountPoint = flag.Arg(0)
	}
	if mountPoint == "" {
		fmt.Fprintln(os.Stderr, "Error: mount_point required")
		flag.Usage()
		return 1
	}

	cacheCfg := cache.Config{
		Enabled:     *cacheEnabled,
		MaxEntries:  *cacheMaxEntries,
		TTL:         *cacheTTL,
		MaxBlobSize: *cacheMaxBlobSize,
	}

	ctx := context.Background()

	var filesystem fs.FS
	fsName := "gitaly"
	fsSubtype := "gitalyfs"
	var skillsClient *skillsclient.Client
	var c *cache.Cache

	switch *mode {
	case "gitaly":
		if cacheCfg.Enabled {
			c = cache.New(cacheCfg)
		}
		if *gitalyAddr == "" || *repoPath == "" {
			fmt.Fprintln(os.Stderr, "Error: -mode=gitaly requires -gitaly and -repo")
			flag.Usage()
			return 1
		}
		gitalyCfg := &gitalyclient.Config{
			Address:      *gitalyAddr,
			StorageName:  *storage,
			RelativePath: *repoPath,
			Branch:       *branch,
			User: &gitalypb.User{
				Name:  []byte(*userName),
				Email: []byte(*userEmail),
			},
			Token: *gitalyToken,
		}
		client, err := gitalyclient.NewClient(ctx, gitalyCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gitaly client: %v\n", err)
			return 1
		}
		defer client.Close()

		repo := gitalyclient.Repo(*storage, *repoPath)
		resolvedBranch := *branch
		if resolvedBranch == "" {
			resolvedBranch, err = client.DefaultBranch(ctx, repo)
			if err != nil {
				fmt.Fprintf(os.Stderr, "default branch: %v\n", err)
				return 1
			}
		}

		cfg := config.Default()
		cfg.GRPCTimeout = *grpcTimeout
		cfg.Cache = cacheCfg

		filesystem = &fuse.GitalyFS{
			Client: client,
			Repo:   repo,
			Branch: resolvedBranch,
			User:   gitalyCfg.User,
			Config: cfg,
			Cache:  c,
		}

	case "skills":
		if *skillsBaseURL == "" || *skillsToken == "" {
			fmt.Fprintln(os.Stderr, "Error: -mode=skills requires -base-url and -skills-token")
			flag.Usage()
			return 1
		}
		// skills 模式用 commit id 判断是否有更新，缓存无需 TTL，设为 0 永不过期
		skillsCacheCfg := cacheCfg
		skillsCacheCfg.TTL = 0
		if skillsCacheCfg.Enabled {
			c = cache.New(skillsCacheCfg)
		}
		skillsClient = skillsclient.NewClient(skillsclient.Config{
			BaseURL:      *skillsBaseURL,
			Token:        *skillsToken,
			UserName:     *skillsUser,
			WorkspaceDir: *workspaceDir,
			HTTPTimeout:  60 * time.Second,
			Ref:          *skillsRef,
		})
		filesystem = &fuse.SkillsFS{
			Client: skillsClient,
			Cache:  c,
		}
		fsName = "skills"
		fsSubtype = "skillsfs"

	default:
		fmt.Fprintf(os.Stderr, "Error: -mode must be gitaly or skills, got %q\n", *mode)
		flag.Usage()
		return 1
	}

	conn, err := bazilfuse.Mount(mountPoint,
		bazilfuse.FSName(fsName),
		bazilfuse.Subtype(fsSubtype),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount: %v\n", err)
		return 1
	}
	defer conn.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- fs.Serve(conn, filesystem)
	}()

	// Skills 模式：技能列表与 LastCommit 分别在不同协程轮询；访问新仓库或新 commit 时更新文件
	var pollerCancel context.CancelFunc
	if skillsClient != nil && (*skillsListPollInterval > 0 || *skillsPollInterval > 0) {
		pollerCtx, cancel := context.WithCancel(context.Background())
		pollerCancel = cancel
		if *skillsListPollInterval > 0 {
			listPoller := skillsclient.NewSkillsListPoller(skillsClient, skillsclient.ListPollerConfig{Interval: *skillsListPollInterval})
			listPoller.OnRefresh = func(skills []skillsclient.Skill) {
				if c == nil {
					return
				}
				var official, user []skillsclient.Skill
				for _, s := range skills {
					if s.BuiltIn {
						official = append(official, s)
					} else {
						user = append(user, s)
					}
				}
				c.Set(cache.KeySkillsList("official"), official)
				c.Set(cache.KeySkillsList("user"), user)
			}
			listPoller.Start(pollerCtx)
		}
		if *skillsPollInterval > 0 {
			commitPoller := skillsclient.NewSkillsCommitPoller(skillsClient, skillsclient.PollerConfig{
				Interval: *skillsPollInterval,
				Ref:      *skillsRef,
			})
			commitPoller.Start(pollerCtx)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	if pollerCancel != nil {
		pollerCancel()
	}
	if err := bazilfuse.Unmount(mountPoint); err != nil {
		fmt.Fprintf(os.Stderr, "unmount: %v\n", err)
		return 1
	}
	if err := <-serveDone; err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

func getEnvInt(key string, def int) int {
	// 简单实现，不解析 env
	return def
}

func getEnvInt64(key string, def int64) int64 {
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
