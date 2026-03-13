// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// skills-sync 对接 AgentHub/CSGHub 技能 API：拉取技能列表并将每个技能 git clone 到本地工作目录。
// 行为与 Python 脚本一致：GET /api/v1/agent/skills，再按 official/user 分别 clone 到 workspace/skills 下。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opcsg/opencsg-fuse-gitaly/internal/skillsclient"
)

func main() {
	os.Exit(run())
}

func run() int {
	baseURL := os.Getenv("CSGHUB_API_BASE_URL")
	token := os.Getenv("CSGHUB_USER_TOKEN")
	userName := os.Getenv("CSGHUB_USER_NAME")
	workspaceDir := os.Getenv("AGENTICHUB_WORKSPACE")
	if workspaceDir == "" {
		workspaceDir = skillsclient.DefaultWorkspaceDir
	}

	markerFile := filepath.Join(workspaceDir, skillsclient.SuccessMarkerFile)
	failureMarkerFile := filepath.Join(workspaceDir, skillsclient.FailureMarkerFile)

	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir workspace: %v\n", err)
		return 1
	}

	if fileExists(markerFile) {
		fmt.Println("Skills already initialized, skipping")
		return 0
	}
	_ = os.Remove(failureMarkerFile)

	if baseURL == "" || token == "" {
		fmt.Println("Skipping skills initialization: missing required environment variables (CSGHUB_API_BASE_URL, CSGHUB_USER_TOKEN)")
		writeMarker(markerFile, "Skills initialization skipped")
		return 0
	}

	cfg := skillsclient.Config{
		BaseURL:      baseURL,
		Token:        token,
		UserName:     userName,
		WorkspaceDir: workspaceDir,
		HTTPTimeout:  60 * time.Second,
	}
	client := skillsclient.NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*60*time.Second) // 10 min total
	defer cancel()

	fmt.Println("Fetching skills from API...")
	skills, err := client.FetchSkills(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch skills: %v\n", err)
		writeMarker(failureMarkerFile, err.Error())
		writeMarker(markerFile, "Skills initialization completed with remote sync errors")
		return 0
	}

	if len(skills) == 0 {
		fmt.Println("No skills found to clone")
		writeMarker(markerFile, "Skills initialization completed: no remote skills found")
		return 0
	}

	var official, user int
	for _, s := range skills {
		if s.BuiltIn {
			official++
		} else {
			user++
		}
	}
	fmt.Printf("Found %d official skills and %d user skills\n", official, user)

	const maxConcurrent = 20
	fmt.Printf("Processing %d skills (max %d at a time)...\n", len(skills), maxConcurrent)
	start := time.Now()
	ok, fail, err := client.SyncAll(ctx, maxConcurrent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
		writeMarker(failureMarkerFile, err.Error())
		writeMarker(markerFile, "Skills initialization completed with errors")
		return 1
	}

	elapsed := time.Since(start)
	fmt.Printf("Processing completed: %d succeeded, %d failed\n", ok, fail)
	fmt.Printf("Total time: %.2fs\n", elapsed.Seconds())

	if fail > 0 {
		writeMarker(markerFile, fmt.Sprintf("Skills initialization completed with %d sync failures", fail))
		return 0
	}
	fmt.Println("Skills initialization process completed")
	writeMarker(markerFile, "Skills initialization completed")
	return 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeMarker(path, message string) {
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(path, []byte(message+"\n"), 0o644)
}
