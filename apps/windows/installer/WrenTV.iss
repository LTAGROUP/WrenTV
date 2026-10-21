; Wren TV for Windows installer. Built by .github/workflows/windows.yml:
;   iscc /DAppVersion=0.1.3 /DSourceDir=..\dist\app /DOutputDir=..\dist WrenTV.iss
; Installs per user (no admin prompt), so updates are a plain re-run.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef SourceDir
  #define SourceDir "..\dist\app"
#endif
#ifndef OutputDir
  #define OutputDir "..\dist"
#endif

[Setup]
AppId={{6C3E1D5A-8F42-4B7E-9A1C-2D7F0E5B9C41}
AppName=Wren TV
AppVersion={#AppVersion}
AppVerName=Wren TV {#AppVersion}
AppPublisher=LTA Group
AppPublisherURL=https://github.com/LTAGROUP/WrenTV
AppSupportURL=https://github.com/LTAGROUP/WrenTV/issues
AppUpdatesURL=https://github.com/LTAGROUP/WrenTV/releases
DefaultDirName={localappdata}\Programs\Wren TV
DefaultGroupName=Wren TV
DisableProgramGroupPage=yes
DisableDirPage=auto
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
; x64 PCs, and Windows on ARM, which gets its own native build below.
ArchitecturesAllowed=x64compatible or arm64
ArchitecturesInstallIn64BitMode=x64compatible or arm64
MinVersion=10.0.17763
OutputDir={#OutputDir}
OutputBaseFilename=WrenTV-Windows-Setup
SetupIconFile=..\WrenTV\Assets\WrenTV.ico
UninstallDisplayIcon={app}\WrenTV.exe
UninstallDisplayName=Wren TV
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[InstallDelete]
; A new version replaces the old files outright (runtime DLLs change between releases).
Type: filesandordirs; Name: "{app}\*"

[Files]
; SourceDir holds the x64 and arm64 publishes side by side.
Source: "{#SourceDir}\x64\*"; DestDir: "{app}"; Check: not IsArm64; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#SourceDir}\arm64\*"; DestDir: "{app}"; Check: IsArm64; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\Wren TV"; Filename: "{app}\WrenTV.exe"
Name: "{autodesktop}\Wren TV"; Filename: "{app}\WrenTV.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\WrenTV.exe"; Description: "{cm:LaunchProgram,Wren TV}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}"
