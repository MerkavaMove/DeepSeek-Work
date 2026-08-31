package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Preset struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Exists   bool   `json:"exists"`
}

// ListPresets 返回当前预设列表：只来自「＋ 添加新的 bat」持久化的 cfg.ExtraBats。
// v1.0.7 起不再扫描 scanFolder（该字段保留，仅作文件对话框默认目录）。
// 按绝对路径去重、按 Name、Path 排序；文件不存在的条目保留（UI 标「缺失」并禁用启动）。
func ListPresets(cfg Config) []Preset {
	seen := map[string]bool{}
	var out []Preset
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		name, sub := ParsePresetName(abs)
		_, statErr := os.Stat(abs)
		out = append(out, Preset{Path: abs, Name: name, Subtitle: sub, Exists: statErr == nil})
	}
	for _, e := range cfg.ExtraBats {
		add(e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// ParsePresetName：展示名 = 文件名去 .bat 与开头序号/分隔符；副标题 = bat 内 MODEL_PATH 的 basename。
func ParsePresetName(batPath string) (name, subtitle string) {
	base := strings.TrimSuffix(filepath.Base(batPath), filepath.Ext(batPath))
	name = strings.TrimLeft(base, "0123456789 _-.")
	if name == "" {
		name = base
	}
	return name, parseModelPath(batPath)
}

func parseModelPath(batPath string) string {
	data, err := os.ReadFile(batPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		idx := strings.Index(strings.ToLower(line), "model_path=")
		if idx < 0 {
			continue
		}
		v := strings.TrimSpace(line[idx+len("model_path="):])
		v = strings.Trim(v, `"`)
		return strings.TrimSuffix(filepath.Base(v), filepath.Ext(v))
	}
	return ""
}
