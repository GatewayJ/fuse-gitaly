// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT
//
// Package cache 提供带 TTL 的 LRU 缓存，用于减少对 Gitaly 的重复请求。
package cache

import (
	"container/list"
	"sync"
	"time"
)

// Config 缓存配置。
type Config struct {
	// MaxEntries 最大条目数，0 表示不限制（不推荐）。
	MaxEntries int
	// TTL 条目过期时间，0 表示永不过期。
	TTL time.Duration
	// MaxBlobSize 超过此大小的 blob 不缓存（字节），0 表示全部缓存。
	MaxBlobSize int64
	// Enabled 是否启用缓存。
	Enabled bool
}

// DefaultConfig 返回默认缓存配置。
func DefaultConfig() Config {
	return Config{
		MaxEntries:  1000,
		TTL:         5 * time.Minute,
		MaxBlobSize: 1 << 20, // 1MB
		Enabled:     true,
	}
}

// Cache 带 TTL 的 LRU 缓存。
type Cache struct {
	mu        sync.RWMutex
	maxEntries int
	ttl       time.Duration
	entries   map[string]*list.Element
	lru       *list.List
}

type entry struct {
	key    string
	value  interface{}
	expiry time.Time
}

// New 创建新缓存。
func New(cfg Config) *Cache {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 1000
	}
	return &Cache{
		maxEntries: cfg.MaxEntries,
		ttl:        cfg.TTL,
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
	}
}

// Get 获取缓存值，过期或不存在返回 (nil, false)。
func (c *Cache) Get(key string) (interface{}, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	ent := elem.Value.(*entry)
	if c.ttl > 0 && time.Now().After(ent.expiry) {
		c.removeElement(elem)
		return nil, false
	}
	c.lru.MoveToBack(elem)
	return ent.value, true
}

// Set 设置缓存值。
func (c *Cache) Set(key string, value interface{}) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry := time.Time{}
	if c.ttl > 0 {
		expiry = time.Now().Add(c.ttl)
	}
	if elem, ok := c.entries[key]; ok {
		c.lru.MoveToBack(elem)
		elem.Value.(*entry).value = value
		elem.Value.(*entry).expiry = expiry
		return
	}
	if c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		c.removeOldest()
	}
	ent := &entry{key: key, value: value, expiry: expiry}
	elem := c.lru.PushBack(ent)
	c.entries[key] = elem
}

// Invalidate 删除指定 key。
func (c *Cache) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.removeElement(elem)
	}
}

// InvalidatePrefix 删除所有以 prefix 开头的 key。
func (c *Cache) InvalidatePrefix(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var toRemove []*list.Element
	for key, elem := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			toRemove = append(toRemove, elem)
		}
	}
	for _, elem := range toRemove {
		c.removeElement(elem)
	}
}

func (c *Cache) removeElement(e *list.Element) {
	c.lru.Remove(e)
	ent := e.Value.(*entry)
	delete(c.entries, ent.key)
}

func (c *Cache) removeOldest() {
	elem := c.lru.Front()
	if elem != nil {
		c.removeElement(elem)
	}
}
