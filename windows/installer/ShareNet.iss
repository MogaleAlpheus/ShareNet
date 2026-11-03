; ShareNet Windows installer (Inno Setup 6).
;
; Build with:  ISCC.exe -DAppVersion=0.2.1 windows\installer\ShareNet.iss
; (ISCC 6.7 rejects the /D form on Windows; use -D.)
; It packs the published folder dist\ShareNet-win-x64, so publish first.

#ifndef AppVersion
  #define AppVersion "0.2.1"
#endif

#define AppName "ShareNet"
#define AppPublisher "ShareNet"
#define AppExe "ShareNet.exe"
#define ProjectUrl "https://github.com/MogaleAlpheus/ShareNet"
#define PublishDir "..\..\dist\ShareNet-win-x64"

[Setup]
; A fixed AppId keeps upgrades in place instead of installing a second copy.
AppId={{B7C4E0F2-8D6A-4E1B-9F2C-3A5D7E9C1B04}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#ProjectUrl}
AppSupportURL={#ProjectUrl}/issues
AppUpdatesURL={#ProjectUrl}/releases
VersionInfoVersion={#AppVersion}
VersionInfoProductName={#AppName}
VersionInfoDescription={#AppName} Setup

DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
UsePreviousAppDir=yes
AllowNoIcons=yes

; The app ships a signed WinTun adapter and edits routes, so it already asks for
; administrator rights on every launch; the installer matches that.
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog

ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763

OutputDir=..\..\dist
OutputBaseFilename=ShareNet-{#AppVersion}-win-x64-setup
SetupIconFile=..\ShareNet\ShareNet.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName={#AppName}
LicenseFile=..\..\LICENSE
WizardStyle=modern
Compression=lzma2/ultra64
SolidCompression=yes

; The app lives in the notification area, so an upgrade has to close it first.
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#PublishDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExe}"; Comment: "Share a phone's VPN connection with this PC"
Name: "{group}\{cm:UninstallProgram,{#AppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Comment: "Share a phone's VPN connection with this PC"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "{cm:LaunchProgram,{#StringChange(AppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Logs are written next to the executable; they are not user settings.
Type: files; Name: "{app}\ShareNet.log"
Type: dirifempty; Name: "{app}\tools\licenses"
Type: dirifempty; Name: "{app}\tools"
Type: dirifempty; Name: "{app}"
