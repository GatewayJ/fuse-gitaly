// Copyright (c) 2025 OpenCSG
// SPDX-License-Identifier: MIT

package gitalyclient

import (
	"path"
	"testing"
)

func TestJoinPath(t *testing.T) {
	tests := []struct {
		base string
		name string
		want string
	}{
		{"", "a", "a"},
		{"dir", "file", "dir/file"},
		{"a/b", "c", "a/b/c"},
	}
	for _, tt := range tests {
		got := JoinPath(tt.base, tt.name)
		if got != tt.want {
			t.Errorf("JoinPath(%q, %q) = %q, want %q", tt.base, tt.name, got, tt.want)
		}
	}
}

func TestJoinPath_StdPath(t *testing.T) {
	// 验证与 path.Join 行为一致
	got := JoinPath("dir", "file")
	want := path.Join("dir", "file")
	if got != want {
		t.Errorf("JoinPath = %q, path.Join = %q", got, want)
	}
}
