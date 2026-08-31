package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	createNewConsole = 0x00000010
	createNoWindow   = 0x08000000
)

// CreateProcessW 直接 P/Invoke。仅用于 StartModel：os/exec 无法表达
// 「不设置 STARTF_USESTDHANDLES」的启动语义（见 StartModel 注释）。
var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCreateProcess = kernel32.NewProc("CreateProcessW")
)

type startupInfoW struct {
	Cb          uint32
	_           [4]byte
	LpReserved  *uint16
	LpDesktop   *uint16
	Flags       uint32
	ShowWindow  uint16
	CbReserved  uint16
	LpReserved2 [8]byte
	StdInput    uintptr
	StdOutput   uintptr
	StdError    uintptr
}

type processInformation struct {
	Process  uintptr
	Thread   uintptr
	Pid      uint32
	ThreadID uint32
}

type Launcher struct {
	cfg       Config
	mu        sync.Mutex
	modelProc *os.Process
}

func NewLauncher(cfg Config) *Launcher { return &Launcher{cfg: cfg} }

// PortOpen 探测 127.0.0.1:port 是否在监听（500ms 超时）。
func (l *Launcher) PortOpen(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// WaitPort 轮询（500ms 间隔）直到端口就绪或超时。
func (l *Launcher) WaitPort(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if l.PortOpen(port) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return l.PortOpen(port)
}

// StartModel 在新控制台窗口运行 bat（保留 llama-server 日志屏）。
//
// 必须直接用 CreateProcessW，不能用 os/exec：Go 1.27 的 os/exec 会对子进程
// 无条件设置 STARTF_USESTDHANDLES 并把未显式指定的 std 流指向 NUL 句柄。
// 于是即使 CREATE_NEW_CONSOLE 打开了可见窗口，bat 的 stdout/stderr 仍被接到
// NUL —— 窗口开了、llama-server 也起来了，但所有输出被静默丢弃（「一键启动
// 控制台无输出」的根因，已用 repro 在句柄级验证：旧路径三个 std 句柄
// GetConsoleMode 全部失败，即全部指向 NUL）。
// 这里不设 STARTF_USESTDHANDLES，系统自动把新控制台的 std 句柄赋给子进程，
// 与用户手动双击 .bat 的语义完全一致。
func (l *Launcher) StartModel(batPath string) error {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\system32\cmd.exe`
	}
	cmdline := fmt.Sprintf(`"%s" /c "%s"`, comspec, batPath)

	appU, err := syscall.UTF16FromString(comspec)
	if err != nil {
		return fmt.Errorf("运行 bat 失败: %w", err)
	}
	cmdU, err := syscall.UTF16FromString(cmdline)
	if err != nil {
		return fmt.Errorf("运行 bat 失败: %w", err)
	}
	dirU, err := syscall.UTF16FromString(filepath.Dir(batPath))
	if err != nil {
		return fmt.Errorf("运行 bat 失败: %w", err)
	}

	var si startupInfoW
	si.Cb = uint32(unsafe.Sizeof(si)) // 全零：不设置任何 dwFlags（尤其不设 STARTF_USESTDHANDLES）
	var pi processInformation
	_, _, e1 := procCreateProcess.Call(
		uintptr(unsafe.Pointer(&appU[0])),
		uintptr(unsafe.Pointer(&cmdU[0])),
		0,     // lpProcessAttributes
		0,     // lpThreadAttributes
		0,     // bInheritHandles
		uintptr(createNewConsole),
		0, // lpEnvironment：继承当前环境
		uintptr(unsafe.Pointer(&dirU[0])),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	// 注意：Go 1.27 的 LazyProc.Call 无条件返回 GetLastError()（成功时是 Errno(0) 而非 nil），
	// 必须按 errno 数值判断成败，不能判 e != nil。
	var errno uint32 = 0xFFFFFFFF
	if n, ok := e1.(syscall.Errno); ok {
		errno = uint32(n)
	} else if e1 != nil {
		return fmt.Errorf("运行 bat 失败: %v", e1)
	}
	if errno != 0 {
		return fmt.Errorf("运行 bat 失败 (CreateProcessW errno=%d)", errno)
	}
	syscall.CloseHandle(syscall.Handle(pi.Process))
	syscall.CloseHandle(syscall.Handle(pi.Thread))

	p, err := os.FindProcess(int(pi.Pid))
	if err != nil {
		return fmt.Errorf("运行 bat 失败: %w", err)
	}
	l.mu.Lock()
	l.modelProc = p
	l.mu.Unlock()
	return nil
}

// StopModel 杀整棵模型进程树。
func (l *Launcher) StopModel() error {
	l.mu.Lock()
	p := l.modelProc
	l.modelProc = nil
	l.mu.Unlock()
	if p == nil {
		return errors.New("没有运行中的模型进程")
	}
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
}

// StartHarness 用传入的完整命令行（可自定义）隐藏窗口启动 harness，记住进程。
// v1.0.14 起命令由调用方显式传入（a.cfg 是唯一事实源）：同一会话内改完参数立即生效，
// 不再依赖 Launcher 启动时按值拷贝的旧 config（改参数后必须重启应用的旧行为）。
// 命令经 cmd.exe /c 执行（CreateProcess 无法直接启动 .cmd，ERROR_BAD_EXE_FORMAT）。
// 若命令第一个 token 看起来是文件路径（含 \ 或盘符）而文件不存在 → 立即报错，不等 30s 超时。
func (l *Launcher) StartHarness(cmdline string) error {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return errors.New("DeepSeek Harness 启动命令为空，请在「DeepSeek Harness」卡片里填写后重试")
	}
	if first, _, _ := strings.Cut(cmdline, " "); first != "" {
		p := strings.Trim(first, `"`)
		if strings.ContainsAny(p, `\:`) {
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("启动命令引用的文件不存在：%s", p)
			}
		}
	}
	cmd := exec.Command("cmd.exe", "/c", cmdline)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 DeepSeek Harness 失败: %w（命令: %s）", err, cmdline)
	}
	// harness 由外部命令自行托管进程（复用端口优先），本工具只负责探测端口就绪，
	// 不跟踪其进程句柄。
	return nil
}

// OpenBrowser 优先 Chrome 独立窗口，缺失则回退系统默认浏览器。
func (l *Launcher) OpenBrowser(url string) error {
	if l.cfg.ChromePath != "" {
		if _, err := os.Stat(l.cfg.ChromePath); err == nil {
			return exec.Command(l.cfg.ChromePath, "--new-window", url).Start()
		}
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
