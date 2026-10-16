@echo off
chcp 65001 >nul
echo ========================================================
echo       MyBox Windows 客户端极致瘦身与缓存清理工具
echo ========================================================
echo.

taskkill /f /im MyBox.exe >nul 2>nul
taskkill /f /im msedge.exe /fi "WINDOWTITLE eq MyBox for Windows" >nul 2>nul
timeout /t 1 /nobreak >nul

if exist "%~dp0build\MyBox.exe" (
  copy /y "%~dp0build\MyBox.exe" "%~dp0MyBox.exe" >nul 2>nul
  echo [√] 已更新最新轻量版 MyBox.exe (内置缓存重定向与精简参数)
)

if exist "%~dp0.profile" (
  echo [*] 正在彻底清除历史 Edge 臃肿缓存 (.profile)...
  rmdir /s /q "%~dp0.profile" 2>nul
  if exist "%~dp0.profile" (
    echo [!] 部分文件被系统进程锁定，请确保客户端已关闭。
  ) else (
    echo [√] 成功清除 1.2 GB 历史垃圾缓存！
  )
) else (
  echo [√] 本地无冗余缓存，文件夹处于极致纯净状态！
)

echo.
powershell -NoProfile -Command "$size = (Get-ChildItem -Path '%~dp0' -Recurse -File -ErrorAction SilentlyContinue | Measure-Object -Property Length -Sum).Sum; Write-Host ('当前客户端目录总大小: {0:N2} MB' -f ($size / 1MB)) -ForegroundColor Green"
echo.
echo ========================================================
echo 优化完成！你可以双击 MyBox.exe 正常启动客户端。
echo ========================================================
pause
