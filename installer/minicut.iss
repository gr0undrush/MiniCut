; Inno Setup 6 script: installs MiniCut together with ffmpeg.
; Build with:  ISCC.exe installer\minicut.iss          (or run installer\build-installer.bat)
; Needs minicut.exe (build.bat) and installer\ffmpeg\{ffmpeg,ffprobe,ffplay}.exe + LICENSE.txt
; (installer\get-ffmpeg.ps1 downloads them).

#ifndef AppVersion
  #define AppVersion "1.1.0"
#endif
#define AppName "MiniCut"
#define AppExe "minicut.exe"

[Setup]
AppId={{B08AB255-1A98-4858-919D-EDE632F81D43}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=MiniCut contributors
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
OutputDir=..\dist
OutputBaseFilename=MiniCut-Setup-{#AppVersion}
SetupIconFile=..\assets\windows\icon.ico
UninstallDisplayIcon={app}\{#AppExe}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
MinVersion=10.0
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
; per-user install by default (no administrator prompt); the user can switch to all users
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
LicenseFile=..\LICENSE

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Additional icons:"

[Files]
Source: "..\minicut.exe"; DestDir: "{app}"; Flags: ignoreversion
; ffmpeg next to minicut.exe: MiniCut uses a copy in its own folder before any ffmpeg in PATH
Source: "ffmpeg\ffmpeg.exe";  DestDir: "{app}"; Flags: ignoreversion
Source: "ffmpeg\ffprobe.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "ffmpeg\ffplay.exe";  DestDir: "{app}"; Flags: ignoreversion
Source: "ffmpeg\LICENSE.txt"; DestDir: "{app}\licenses"; DestName: "FFmpeg-LICENSE.txt"; Flags: ignoreversion
Source: "THIRD-PARTY.txt";    DestDir: "{app}\licenses"; Flags: ignoreversion
Source: "..\LICENSE";         DestDir: "{app}\licenses"; DestName: "MiniCut-LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "Start {#AppName}"; Flags: nowait postinstall skipifsilent
