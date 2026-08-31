package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx      context.Context
	cfg      Config
	launcher *Launcher
	waiting  atomic.Bool
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	migrateLegacyConfig() // 旧位置 %APPDATA%\ai-starter 的 config 自动复制到新位置
	a.cfg = LoadConfig(configPath())
	a.launcher = NewLauncher(a.cfg)
	a.emit("idle", "空闲", false, false)
}

// emit 向前端推送 status 事件（stage: idle|starting|ready|timeout|error）。
func (a *App) emit(stage, text string, canSkip, canStop bool) {
	runtime.EventsEmit(a.ctx, "status", map[string]any{
		"stage": stage, "text": text, "canSkip": canSkip, "canStop": canStop,
	})
}

// GetSysInfo 返回当前系统信息（前端启动时调用一次，不实时刷新）。
func (a *App) GetSysInfo() SysInfo { return CollectSysInfo() }

// EnsureHarness 确保 harnessPort 上 harness 可用（复用优先；未监听则用配置的自定义命令启动，等 30s）。
// v1.0.11：就绪后由前端打开独立 Chrome 新页面（恢复 v1.0.7 前的浏览器方式，移除内嵌 iframe）。
func (a *App) EnsureHarness() error {
	if a.launcher.PortOpen(a.cfg.HarnessPort) {
		return nil
	}
	if err := a.launcher.StartHarness(a.cfg.HarnessCmd); err != nil {
		return err
	}
	if !a.launcher.WaitPort(a.cfg.HarnessPort, 30*time.Second) {
		return errors.New("DeepSeek Harness 启动超时（30s），请手动运行启动命令检查")
	}
	return nil
}

// OpenHarnessBrowser 用独立 Chrome 窗口打开 harness 页面（3080）。
func (a *App) OpenHarnessBrowser() error {
	return a.launcher.OpenBrowser("http://127.0.0.1:" + strconv.Itoa(a.cfg.HarnessPort))
}

// SetHarnessCmd 持久化自定义启动命令到 config.json。
func (a *App) SetHarnessCmd(cmd string) error {
	a.cfg.HarnessCmd = strings.TrimSpace(cmd)
	return a.cfg.Save(configPath())
}

func (a *App) GetConfig() Config { return a.cfg }

// ListPresets 当前预设（只来自「＋ 添加新的 bat」持久化的 ExtraBats，不扫描目录）。
func (a *App) ListPresets() []Preset {
	return ListPresets(a.cfg)
}

// AddBat 弹原生对话框选 .bat，持久化到 config.json，返回所选路径。
// 注（偏离 D2，2026-07-21）：本机固定依赖 wails v2.15.0 已移除 pkg/dialogs 包，
// 原生打开对话框的 Go API 为 runtime.OpenFileDialog（字段一一对应：
// dialogs.Options→runtime.OpenDialogOptions、FileFilter.Name→DisplayName），
// 行为与简报逐字稿完全一致（标题/默认目录/过滤/取消文案/保存路径不变）。
func (a *App) AddBat() (string, error) {
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "选择 bat 启动脚本",
		DefaultDirectory: a.cfg.ScanFolder,
		Filters:          []runtime.FileFilter{{DisplayName: "bat 文件", Pattern: "*.bat"}},
	})
	if err != nil || p == "" {
		return "", errors.New("已取消")
	}
	a.cfg.ExtraBats = append(a.cfg.ExtraBats, p)
	if err := a.cfg.Save(configPath()); err != nil {
		return "", fmt.Errorf("保存配置失败: %w", err)
	}
	return p, nil
}

// RemoveBat 从持久化预设中删除指定路径（预设行的「删除」按钮；不删 bat 文件本身）。
func (a *App) RemoveBat(path string) error {
	kept := a.cfg.ExtraBats[:0]
	for _, p := range a.cfg.ExtraBats {
		if p != path {
			kept = append(kept, p)
		}
	}
	a.cfg.ExtraBats = kept
	return a.cfg.Save(configPath())
}

// StartModel 启动本地模型；8080 已被占用则返回 "port-busy" 由前端询问。
// 返回阶段串："running"（后台轮询已启动）| "port-busy" | "already"（正在等待中）。
func (a *App) StartModel(presetPath string) (string, error) {
	if a.waiting.Load() {
		return "already", nil
	}
	if a.launcher.PortOpen(a.cfg.ModelPort) {
		return "port-busy", nil
	}
	if err := a.launcher.StartModel(presetPath); err != nil {
		return "error", err
	}
	a.waiting.Store(true)
	a.emit("starting", fmt.Sprintf("模型启动中… 等待 %d 就绪", a.cfg.ModelPort), true, true)
	go a.waitModelReady()
	return "running", nil
}

// waitModelReady 每 1s 轮询 8080，最长 180s；就绪后自动打开 harness。
func (a *App) waitModelReady() {
	for i := 0; i < 180; i++ {
		if !a.waiting.Load() {
			return // 被跳过或停止
		}
		if a.launcher.PortOpen(a.cfg.ModelPort) {
			a.waiting.Store(false)
			if err := a.EnsureHarness(); err != nil {
				a.emit("error", "模型就绪，但启动 DeepSeek Harness 失败：" + err.Error(), false, true)
				return
			}
			a.emit("ready", "✓ 模型就绪，DeepSeek Harness 已就绪（独立 Chrome 页面已打开）", false, true)
			return
		}
		time.Sleep(time.Second)
	}
	a.waiting.Store(false)
	a.emit("timeout", "启动超时（180s），请查看模型控制台窗口", false, true)
}

// StartBatOnly 只启动所选 bat（「单独启动 bat」）：不轮询端口、不启动 DeepSeek Harness。
// 返回阶段串同 StartModel："running" | "port-busy" | "already" | "error"。
func (a *App) StartBatOnly(presetPath string) (string, error) {
	if a.waiting.Load() {
		return "already", nil
	}
	if a.launcher.PortOpen(a.cfg.ModelPort) {
		return "port-busy", nil
	}
	if err := a.launcher.StartModel(presetPath); err != nil {
		return "error", err
	}
	a.emit("bat-only", "✓ bat 已启动（未启动 DeepSeek Harness，需要时可点「启动 DeepSeek Harness」）", false, true)
	return "running", nil
}

// SkipWait 放弃等待，直接确保 harness 就绪。
func (a *App) SkipWait() error {
	a.waiting.Store(false)
	if err := a.EnsureHarness(); err != nil {
		return err
	}
	a.emit("ready", "✓ 已跳过等待，DeepSeek Harness 已就绪（独立 Chrome 页面已打开）", false, true)
	return nil
}

// StopModel 停止模型（整棵进程树），并结束等待。
func (a *App) StopModel() error {
	a.waiting.Store(false)
	if err := a.launcher.StopModel(); err != nil {
		return err
	}
	a.emit("idle", "模型已停止", false, false)
	return nil
}

// QuitApp 退出；stopModel=true 时先停模型。
func (a *App) QuitApp(stopModel bool) error {
	if stopModel {
		_ = a.launcher.StopModel()
	}
	runtime.Quit(a.ctx)
	return nil
}
