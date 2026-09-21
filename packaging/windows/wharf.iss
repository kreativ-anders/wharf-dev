; Built Windows bundle (wharf_gui.exe + data\ + DLLs + wharfd.exe) → one
; installer, Wharf-<version>-windows-x64-setup.exe: Program Files, a Start menu
; entry and an uninstaller. A port of finanzgecko's packaging/windows/finanzgecko.iss;
; see dev/releasing.md §5.
;
;   iscc /DAppVersion=<version> /DBuildDir=<...\runner\Release> /DOutputName=<name> wharf.iss
;
; INFO: Nothing Wharf keeps lives next to the exe — the root is %USERPROFILE%\Wharf
; (daemon/internal/layout) — so a read-only Program Files folder is fine, and
; uninstalling leaves the user's projects, PHP builds and config alone.

#ifndef AppVersion
  #error "AppVersion is not set — pass /DAppVersion=X.Y.Z"
#endif
#ifndef BuildDir
  #error "BuildDir is not set — pass /DBuildDir=<...\runner\Release>"
#endif
#ifndef OutputName
  #define OutputName "Wharf-" + AppVersion + "-windows-x64-setup"
#endif

#define AppExeName "wharf_gui.exe"

[Setup]
; WARNING: Never change the AppId: Windows would treat the next version as a
; different application and install it beside this one instead of over it.
AppId={{69CA8845-9DD6-4189-8753-C41AEC6FFA53}
AppName=Wharf
AppVersion={#AppVersion}
AppPublisher=Manuel Steinberg (kreativ-anders)
AppPublisherURL=https://github.com/kreativ-anders/wharf-dev
DefaultDirName={autopf}\Wharf
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#AppExeName}
OutputBaseFilename={#OutputName}
OutputDir=.
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
SetupIconFile=..\..\gui\windows\runner\resources\app_icon.ico
; INFO: An update over a running Wharf asks it to close first, so wharfd.exe is
; not locked mid-copy.
CloseApplications=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#BuildDir}\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs ignoreversion

[Icons]
Name: "{autoprograms}\Wharf"; Filename: "{app}\{#AppExeName}"
Name: "{autodesktop}\Wharf"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,Wharf}"; Flags: nowait postinstall skipifsilent
