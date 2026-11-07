#ifndef AppVersion
 #error AppVersion is required
#endif
#ifndef PayloadDir
 #error PayloadDir is required
#endif
#ifndef OutputPath
 #error OutputPath is required
#endif

[Setup]
AppId=io.github.Presikid.KurumiFRP
AppName=KurumiFRP
AppVersion={#AppVersion}
AppPublisher=Presikid
AppPublisherURL=https://github.com/Presikid/KurumiFrp
DefaultDirName={commonpf}\KurumiFRP
UsePreviousAppDir=no
SetupMutex=Global\KurumiFRPSetupMachine
DefaultGroupName=KurumiFRP
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible or arm64
ArchitecturesInstallIn64BitMode=x64compatible or arm64
MinVersion=10.0
WizardStyle=modern
DisableDirPage=no
DisableProgramGroupPage=yes
OutputDir={#OutputPath}
OutputBaseFilename=KurumiFRP-{#AppVersion}-Setup
Compression=lzma2
SolidCompression=yes
UninstallDisplayName=KurumiFRP
UninstallDisplayIcon={app}\kurumi.exe
CloseApplications=no
RestartApplications=no
SetupLogging=no
VersionInfoDescription=KurumiFRP graphical installer
VersionInfoProductName=KurumiFRP

[Tasks]
Name: desktopicon; Description: "Create a desktop shortcut"; GroupDescription: "Shortcuts:"
Name: autostart; Description: "Run a private workbench for each user when they sign in"; GroupDescription: "Startup:"; Flags: unchecked
Name: cleanup; Description: "Delete this Setup.exe after successful installation"; GroupDescription: "Installer cleanup:"

[Files]
Source: "{#PayloadDir}\amd64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: not IsArm64
Source: "{#PayloadDir}\arm64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: IsArm64
Source: "{#PayloadDir}\INSTALL.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"
Name: "{group}\Uninstall KurumiFRP"; Filename: "{uninstallexe}"
Name: "{commondesktop}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"; Tasks: desktopicon
Name: "{commonstartup}\KurumiFRP"; Filename: "{app}\kurumi.exe"; WorkingDir: "{app}"; Tasks: autostart

[Run]
Filename: "{app}\kurumi.exe"; Parameters: "--open"; Description: "Open KurumiFRP"; Flags: postinstall nowait skipifsilent

[UninstallDelete]
Type: files; Name: "{app}\native-install.json"
Type: files; Name: "{app}\.uninstalling"

[Code]
var
  Ready: Boolean;

function GetFileAttributesW(lpFileName: String): LongWord;
  external 'GetFileAttributesW@kernel32.dll stdcall';

function PlainDestination(Path: String): Boolean;
var Attr: LongWord; Parent: String;
begin
  Result := True;
  while Length(Path) > 3 do begin
    Attr := GetFileAttributesW(Path);
    if (Attr <> $FFFFFFFF) and ((Attr and $400) <> 0) then begin Result := False; Exit; end;
    Parent := ExtractFileDir(Path);
    if Parent = Path then Exit;
    Path := Parent;
  end;
end;

function InitializeSetup(): Boolean;
begin
  Result := not RegKeyExists(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1');
  if not Result then MsgBox('A legacy per-user installation is present. Back up its data and cores and remove it before installing the machine-wide edition. No legacy data was changed.', mbError, MB_OK);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var Code: Integer; App, Exe, RegisteredDir: String;
begin
  Result := '';
  App := ExpandConstant('{app}');
  Exe := App + '\kurumi.exe';
  if not PlainDestination(App) then begin
    Result := 'Linked installation directories are not supported. No files were changed.'; Exit;
  end;
  if (DirExists(App + '\data') or FileExists(Exe)) and not FileExists(App + '\native-install.json') then begin
    Result := 'This directory contains an old script installation or unmanaged files. Choose a new directory. See INSTALL.md for migration.'; Exit;
  end;
  if FileExists(Exe) then begin
    if not RegQueryStringValue(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1', 'InstallLocation', RegisteredDir) then begin
      Result := 'An executable exists here but no machine installation is registered. Choose a clean directory.'; Exit;
    end;
    if CompareText(RemoveBackslash(RegisteredDir), RemoveBackslash(App)) <> 0 then begin
      Result := 'The existing executable is not in the registered machine installation. No executable was run.'; Exit;
    end;
    if not Exec(Exe, '--native-maintenance stop', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then Result := 'The running workbench could not be stopped safely. Existing data was retained.';
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer; App, Exe: String;
begin
  if CurStep = ssPostInstall then begin
    App := ExpandConstant('{app}'); Exe := App + '\kurumi.exe';
    if not Exec(Exe, '--native-maintenance init', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException('Installation receipt could not be initialized. Setup.exe was retained.');
    if not ExecAsOriginalUser(Exe, '', App, SW_HIDE, ewNoWait, Code) then RaiseException('Could not start KurumiFRP.');
    if not ExecAsOriginalUser(Exe, '--check-running', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException('The local API did not become ready. Setup.exe was retained; existing configuration was not deleted.');
    Ready := True;
  end;
end;

procedure DeinitializeSetup();
var Code: Integer; Source, Args: String;
begin
  if Ready and WizardIsTaskSelected('cleanup') then begin
    Source := ExpandConstant('{srcexe}');
    Args := '--cleanup-setup "' + Source + '" --cleanup-sha256 ' + Lowercase(GetSHA256OfFile(Source));
    if not ExecAsOriginalUser(ExpandConstant('{app}\kurumi.exe'), Args, ExpandConstant('{app}'), SW_HIDE, ewNoWait, Code) then
      MsgBox('Setup finished, but its downloaded EXE could not be removed. You may delete that EXE manually.', mbInformation, MB_OK);
  end;
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
  if not UninstallSilent then Result := MsgBox('Uninstall KurumiFRP for all users and permanently delete all accounts configurations, authentication keys, downloaded frp cores and application logs?', mbConfirmation, MB_YESNO) = IDYES;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var Code: Integer;
begin
  if CurUninstallStep = usUninstall then begin
    if not Exec(ExpandConstant('{app}\kurumi.exe'), '--native-maintenance purge', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException('Shutdown or data removal was not confirmed. Uninstall stopped; retry after resolving the error. No process was killed by name.');
  end;
end;
