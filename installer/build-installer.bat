@echo off
rem Builds dist\MiniCut-Setup-<version>.exe (installer with ffmpeg) and dist\MiniCut-portable.zip.
rem Needs: Go + MinGW (see README), Inno Setup 6 (https://jrsoftware.org/isinfo.php), internet (first run).
cd /d "%~dp0\.."

call build.bat
if errorlevel 1 exit /b 1

powershell -NoProfile -ExecutionPolicy Bypass -File installer\get-ffmpeg.ps1
if errorlevel 1 exit /b 1

set ISCC=
for %%P in ("%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe" "%ProgramFiles%\Inno Setup 6\ISCC.exe" "%LOCALAPPDATA%\Programs\Inno Setup 6\ISCC.exe") do (
    if exist %%P set ISCC=%%~P
)
if not defined ISCC (
    echo Inno Setup 6 was not found. Install it from https://jrsoftware.org/isinfo.php and run this again.
    exit /b 1
)

if not exist dist mkdir dist
"%ISCC%" %* installer\minicut.iss
if errorlevel 1 exit /b 1

rem portable version: no installation, ffmpeg next to minicut.exe
if exist dist\MiniCut-portable.zip del dist\MiniCut-portable.zip
powershell -NoProfile -Command "Compress-Archive -Force -DestinationPath dist\MiniCut-portable.zip -Path minicut.exe,installer\ffmpeg\ffmpeg.exe,installer\ffmpeg\ffprobe.exe,installer\ffmpeg\ffplay.exe,installer\ffmpeg\LICENSE.txt,LICENSE,installer\THIRD-PARTY.txt"
if errorlevel 1 exit /b 1

echo.
echo Done: see the dist folder.
exit /b 0
