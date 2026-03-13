// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT

package cache

// 缓存 key 前缀，用于 InvalidatePrefix。
const (
	PrefixTree = "tree:"
	PrefixMeta = "meta:"
	PrefixBlob = "blob:"
)

// KeyTree 目录树条目缓存 key。
func KeyTree(branch, path string) string {
	return PrefixTree + branch + ":" + path
}

// KeyMeta 文件元数据缓存 key，值为 int64 (size)。
func KeyMeta(branch, path string) string {
	return PrefixMeta + branch + ":" + path
}

// KeyBlob blob 内容缓存 key（按 OID）。
func KeyBlob(oid string) string {
	return PrefixBlob + oid
}

// KeyBlobPath 按路径的 blob 缓存 key（无 OID 时用）。
func KeyBlobPath(branch, path string) string {
	return PrefixBlob + "path:" + branch + ":" + path
}

// PrefixSkills 技能列表缓存前缀。
const PrefixSkills = "skills:"

// KeySkillsList 技能 API 列表缓存 key，category 为 "official" 或 "user"。
func KeySkillsList(category string) string {
	return PrefixSkills + "list:" + category
}
