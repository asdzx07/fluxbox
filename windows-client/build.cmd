@echo off
setlocal

set CSC="C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if not exist %CSC% set CSC="C:\Windows\Microsoft.NET\Framework\v4.0.30319\csc.exe"

if not exist "%~dp0build" mkdir "%~dp0build"

if exist %CSC% (
  echo Using .NET Framework csc compiler: %CSC%
  %CSC% /target:winexe /optimize+ /win32icon:"%~dp0app.ico" /win32manifest:"%~dp0app.manifest" /out:"%~dp0build\MyBox.exe" "%~dp0Launcher.cs"
  if errorlevel 1 exit /b %errorlevel%
  copy /y "%~dp0build\MyBox.exe" "%~dp0MyBox.exe" >nul 2>nul
  if errorlevel 1 (
    echo [提示] 发现 MyBox.exe 当前正在运行，最新编译程序已生成于: "%~dp0build\MyBox.exe"
    echo        退出运行中的客户端后即可一键替换覆盖。
  ) else (
    echo Build successful: "%~dp0MyBox.exe"
  )
  endlocal
  exit /b 0
)

where msbuild >nul 2>nul
if errorlevel 1 (
  echo C# compiler was not found.
  exit /b 1
)

msbuild "%~dp0MyBox.csproj" /t:Rebuild /p:Configuration=Release /p:Platform=AnyCPU
if errorlevel 1 exit /b %errorlevel%

echo Build output: "%~dp0build\Release\MyBox.exe"
endlocal
