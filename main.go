package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "DeepSeek Work",
		Width:     460,
		Height:    760,
		MinWidth:  400,
		MinHeight: 640,
		Frameless: true, // 无边框；自定义标题栏可拖
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 透明窗口：alpha=0 的窗口背景。
		// 依据本机 wails v2.15.0 源码 pkg/options/options.go（第 48-50 行）：
		// BackgroundColour 是 options.App 顶层字段，类型为 *options.RGBA，
		// 官方注释要求用 options.NewRGBA 构造。
		BackgroundColour: options.NewRGBA(0, 0, 0, 0),
		// WebView2 浏览器数据目录（v1.0.13）：与 config 同目录（%APPDATA%\DeepSeek Work），
		// 路径运行时用 os.Getenv 动态拼接，不写死；不设置时 wails 默认放在
		// %APPDATA%\<exe 名>.exe，会多出一个与 exe 同名的杂散目录。
		Windows:   &windows.Options{WebviewUserDataPath: filepath.Join(os.Getenv("APPDATA"), appDataDir)},
		OnStartup: app.startup,
		Bind:      []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
