@echo off
rem Builds minicut.exe with the app icon embedded and no console window.
rem Needs Go and MinGW-w64 (gcc + windres) in PATH. Run from anywhere.
cd /d "%~dp0"
if not exist go.sum go mod tidy

pushd assets\windows
windres -O coff -i app.rc -o ..\..\cmd\minicut\app_windows_amd64.syso
set RC=%errorlevel%
popd
if not "%RC%"=="0" goto :err

go build -ldflags "-H=windowsgui" -o minicut.exe .\cmd\minicut
if errorlevel 1 goto :err
echo Built minicut.exe
exit /b 0

:err
echo Build failed
exit /b 1
