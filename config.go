package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	// ScanFolder v1.0.7 起不再用于扫描，仅作「添加新的 bat」对话框的默认目录。
	ScanFolder  string   `json:"scanFolder"`
	ExtraBats   []string `json:"extraBats"`
	ModelPort   int      `json:"modelPort"`
	HarnessPort int      `json:"harnessPort"`
	ChromePath  string   `json:"chromePath"`
	// HarnessCmd v1.0.7：harness 完整启动命令行（经 cmd.exe /c 执行，隐藏窗口）。
	// 用户可改成任意命令（别的 web 服务、别的端口等），启动前探测 HarnessPort 是否已监听。
	HarnessCmd string `json:"harnessCmd"`
}

func defaultConfig() Config {
	return Config{
		ScanFolder:  `D:\Program Files\Llama`,
		ModelPort:   8080,
		HarnessPort: 3080,
		ChromePath:  `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		HarnessCmd:  `C:\Users\2644853\AppData\Roaming\npm\dsh.cmd web --no-open --port 3080`,
	}
}

// LoadConfig 读 path；缺失/损坏/部分字段为空 → 用默认值补齐或整体重建。
func LoadConfig(path string) Config {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	// 直接在默认值上 unmarshal：合法 JSON 缺字段保留默认；非法 JSON → 重建默认
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig()
	}
	return cfg
}

// Save 写 JSON 到 path（自动建目录）。
func (c Config) Save(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// appDataDir 本应用所有用户数据统一所在目录名（config.json + WebView2 浏览器数据），
// 完整路径运行时用 os.Getenv("APPDATA") 动态拼接，不写死任何用户路径。
const appDataDir = "DeepSeek Work"

// configPath 实际配置位置：固定在 C 盘用户 AppData（%APPDATA%\DeepSeek Work\config.json，目录名与 exe 一致）。
func configPath() string {
	return filepath.Join(os.Getenv("APPDATA"), appDataDir, "config.json")
}

// migrateLegacyConfig 一次性迁移：旧版 config 在 %APPDATA%\ai-starter\config.json，
// 新位置没有 config 而旧位置有时，把旧文件复制过来（不覆盖新位置已有配置，也不删旧文件）。
func migrateLegacyConfig() {
	newPath := configPath()
	if _, err := os.Stat(newPath); err == nil {
		return // 新位置已有配置 → 不动
	}
	oldPath := filepath.Join(os.Getenv("APPDATA"), "ai-starter", "config.json")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return // 旧位置也没有 → 全新安装
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return
	}
	_ = os.WriteFile(newPath, data, 0644)
}
