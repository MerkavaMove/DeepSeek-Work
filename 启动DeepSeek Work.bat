@echo off
rem DeepSeek Work —— 单独启动脚本（项目根目录入口，启动 build\bin\DeepSeek Work.exe）
cd /d "%~dp0"
if exist "build\bin\DeepSeek Work.exe" (
    start "" "build\bin\DeepSeek Work.exe"
) else (
    echo 未找到 build\bin\DeepSeek Work.exe，请先执行 wails build
    pause
)
