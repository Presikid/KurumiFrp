#ifndef AppVersion
 #error AppVersion is required
#endif
#ifndef PayloadDir
 #error PayloadDir is required
#endif
#ifndef OutputPath
 #error OutputPath is required
#endif
#ifndef BundledAMD64SHA
 #error BundledAMD64SHA is required
#endif
#ifndef BundledARM64SHA
 #error BundledARM64SHA is required
#endif

[Setup]
AppId=io.github.Presikid.KurumiFRP
AppName=KurumiFRP
AppVersion={#AppVersion}
AppPublisher=Presikid
AppPublisherURL=https://github.com/Presikid/KurumiFrp
DefaultDirName={commonpf}\KurumiFRP
UsePreviousAppDir=yes
UninstallLogMode=new
SetupMutex=Global\KurumiFRPSetupMachine
DefaultGroupName=KurumiFRP
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible or arm64
ArchitecturesInstallIn64BitMode=x64compatible or arm64
MinVersion=10.0
WizardStyle=modern
WizardImageFile=wizard.bmp
WizardSmallImageFile=wizard-small.bmp
SetupIconFile=kurumi.ico
DisableDirPage=no
DisableProgramGroupPage=yes
OutputDir={#OutputPath}
OutputBaseFilename=KurumiFRP-{#AppVersion}-Setup
Compression=lzma2
SolidCompression=yes
UninstallDisplayName=KurumiFRP
UninstallDisplayIcon={app}\kurumi.ico
CloseApplications=no
RestartApplications=no
SetupLogging=no
VersionInfoDescription=KurumiFRP graphical installer
VersionInfoProductName=KurumiFRP

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[Messages]
english.FinishedLabel=KurumiFRP was installed. Sign in to the account you normally use and open KurumiFRP from the Start menu without administrator privileges.
english.FinishedLabelNoIcons=KurumiFRP was installed. Sign in to the account you normally use and open KurumiFRP without administrator privileges.
chinesesimplified.FinishedLabel=KurumiFRP 已安装。请登录平时使用的 Windows 账户，通过开始菜单以普通权限手动打开 KurumiFRP。
chinesesimplified.FinishedLabelNoIcons=KurumiFRP 已安装。请登录平时使用的 Windows 账户，以普通权限手动打开 KurumiFRP。

[CustomMessages]
english.ShortcutGroup=Shortcuts:
english.StartupGroup=Startup:
english.DesktopTask=Create a desktop shortcut
english.AutostartTask=Start a private workbench when each user signs in
chinesesimplified.ShortcutGroup=快捷方式：
chinesesimplified.StartupGroup=启动选项：
chinesesimplified.DesktopTask=创建桌面快捷方式
chinesesimplified.AutostartTask=用户登录时启动各自的工作台

english.LegacyInstall=A legacy per-user installation is present. Back up its data and cores and remove it before installing the machine-wide edition. No legacy data was changed.
chinesesimplified.LegacyInstall=检测到旧版用户级安装。请先备份配置与核心并卸载旧版，再安装当前版本。旧数据未被修改。
english.LinkedPath=Linked installation directories are not supported. No files were changed.
chinesesimplified.LinkedPath=不支持链接或快捷方式指向的安装目录。未修改任何文件。
english.UnmanagedDir=This directory contains an old script installation or unmanaged files. Choose a new directory. See INSTALL.md for migration.
chinesesimplified.UnmanagedDir=此目录包含旧版安装或未受管理的文件。请选择新目录，迁移方法请参阅 INSTALL.md。
english.UnregisteredExe=No machine installation is registered, or the selected directory is not empty. Existing files were retained.
chinesesimplified.UnregisteredExe=未找到有效的机器级安装登记，或所选目录不是空目录。现有文件已保留。
english.WrongInstallDir=The selected directory differs from the registered machine installation. Choose the registered directory; existing data was retained.
chinesesimplified.WrongInstallDir=所选目录与已注册的机器级安装目录不同。请选择已注册目录，原有数据已保留。
english.UnsafeTarget=The installation directory, its permissions, or its existing data could not be verified. Use a protected local directory and inspect the existing data. Nothing was migrated or deleted.
chinesesimplified.UnsafeTarget=无法确认安装目录、访问权限或现有数据的安全性。请检查原数据并使用受保护的本地目录；未迁移或删除原数据。
english.UnsafeTemp=Setup needs its temporary directory on a local volume with persistent ACLs and no linked path. Nothing was migrated or deleted.
chinesesimplified.UnsafeTemp=安装程序的临时目录必须位于支持持久 ACL 的本地卷，路径不能包含链接。未迁移或删除原数据。
english.StopFailed=The running workbench could not be stopped safely. Existing data was retained.
chinesesimplified.StopFailed=无法安全停止正在运行的工作台。现有数据已保留。
english.InitFailed=Installation receipt could not be initialized. Setup.exe was retained.
chinesesimplified.InitFailed=无法初始化安装记录，安装程序已保留。
english.ManualStart=Installation completed. Sign in to your usual Windows account and open KurumiFRP without administrator privileges.
chinesesimplified.ManualStart=安装完成。请登录平时使用的 Windows 账户，以普通权限手动打开 KurumiFRP。
english.BundledMismatch=This same-version installer contains a different bundled core. Install a newer application version instead; the current installation was not changed.
chinesesimplified.BundledMismatch=此安装包与已安装的同版本程序所含预置核心不同。请安装更新的软件版本；现有安装未被修改。
english.UninstallConfirm=Close the workbench and its connections for every account before uninstalling. Application files will be removed; account data will be retained. Continue?
chinesesimplified.UninstallConfirm=卸载前请关闭所有账户的工作台及连接。卸载会移除程序文件，保留各账户的数据。是否继续？

[Tasks]
Name: desktopicon; Description: "{cm:DesktopTask}"; GroupDescription: "{cm:ShortcutGroup}"
Name: autostart; Description: "{cm:AutostartTask}"; GroupDescription: "{cm:StartupGroup}"; Flags: unchecked

[Files]
Source: "{#PayloadDir}\amd64\kurumi.exe"; DestName: "maintenance-amd64.exe"; Flags: dontcopy noencryption
Source: "{#PayloadDir}\arm64\kurumi.exe"; DestName: "maintenance-arm64.exe"; Flags: dontcopy noencryption
Source: "{#PayloadDir}\amd64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: not IsArm64
Source: "{#PayloadDir}\arm64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: IsArm64
Source: "{#PayloadDir}\amd64\bundled\{#AppVersion}\*"; DestDir: "{app}\bundled\{#AppVersion}"; Flags: onlyifdoesntexist; Check: not IsArm64
Source: "{#PayloadDir}\arm64\bundled\{#AppVersion}\*"; DestDir: "{app}\bundled\{#AppVersion}"; Flags: onlyifdoesntexist; Check: IsArm64
Source: "{#PayloadDir}\INSTALL.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "kurumi.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"; IconFilename: "{app}\kurumi.ico"
Name: "{group}\Uninstall KurumiFRP"; Filename: "{uninstallexe}"
Name: "{commondesktop}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"; IconFilename: "{app}\kurumi.ico"; Tasks: desktopicon
Name: "{commonstartup}\KurumiFRP"; Filename: "{app}\kurumi.exe"; WorkingDir: "{app}"; Tasks: autostart

[Code]
var
  Helper: String;

function GetFileAttributesW(lpFileName: String): LongWord;
  external 'GetFileAttributesW@kernel32.dll stdcall';
function GetDriveTypeW(Root: String): LongWord;
  external 'GetDriveTypeW@kernel32.dll stdcall';
function QueryDosDeviceW(DeviceName, TargetPath: String; MaxLength: LongWord): LongWord;
  external 'QueryDosDeviceW@kernel32.dll stdcall';
function GetVolumeInformationW(Root, VolumeName: String; VolumeNameSize: LongWord;
  var Serial, MaxComponent, Flags: LongWord; FileSystemName: String; FileSystemNameSize: LongWord): Boolean;
  external 'GetVolumeInformationW@kernel32.dll stdcall';

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

function SafeTempVolume(Path: String): Boolean;
var Drive, Mapping, Prefix, Suffix: String; Kind, Serial, MaxComponent, Flags: LongWord; I: Integer;
begin
  Result := False;
  Drive := ExtractFileDrive(Path);
  if (Length(Drive) <> 2) or not PlainDestination(Path) then Exit;
  SetLength(Mapping, 1024);
  if QueryDosDeviceW(Drive, Mapping, Length(Mapping)) = 0 then Exit;
  I := Pos(#0, Mapping);
  if I = 0 then Exit;
  Mapping := Copy(Mapping, 1, I - 1);
  Prefix := '\Device\HarddiskVolume';
  if CompareText(Copy(Mapping, 1, Length(Prefix)), Prefix) <> 0 then Exit;
  Suffix := Copy(Mapping, Length(Prefix) + 1, Length(Mapping));
  if Suffix = '' then Exit;
  for I := 1 to Length(Suffix) do
    if (Suffix[I] < '0') or (Suffix[I] > '9') then Exit;
  Drive := Drive + '\';
  Kind := GetDriveTypeW(Drive);
  if (Kind <> 2) and (Kind <> 3) then Exit;
  if GetVolumeInformationW(Drive, '', 0, Serial, MaxComponent, Flags, '', 0) then
    Result := (Flags and 8) <> 0;
end;

function EmptyDirectory(Path: String): Boolean;
var Entry: TFindRec;
begin
  Result := not DirExists(Path);
  if Result then Exit;
  if not FindFirst(Path + '\*', Entry) then Exit;
  Result := True;
  try
    repeat
      if (Entry.Name <> '.') and (Entry.Name <> '..') then begin Result := False; Break; end;
    until not FindNext(Entry);
  finally
    FindClose(Entry);
  end;
end;

function InitializeSetup(): Boolean;
begin
  Result := not RegKeyExists(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1');
  if not Result then SuppressibleMsgBox(ExpandConstant('{cm:LegacyInstall}'), mbError, MB_OK, IDOK);
end;

function RunHelper(Args: String): Boolean;
var Code: Integer;
begin
  Result := ExecAndLogOutput(Helper, Args + ' --install-dir "' + ExpandConstant('{app}') + '"',
    ExpandConstant('{tmp}'), SW_HIDE, ewWaitUntilTerminated, Code, nil) and (Code = 0);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var App, Exe, RegisteredDir, PreviousVersion, ExpectedManifest, HelperName: String; Registered, Reinstall: Boolean;
begin
  Result := '';
  App := ExpandConstant('{app}');
  Exe := App + '\kurumi.exe';
  if not PlainDestination(App) then begin
    Result := ExpandConstant('{cm:LinkedPath}'); Exit;
  end;
  if not PlainDestination(App + '\bundled\{#AppVersion}') or not PlainDestination(App + '\bundled\{#AppVersion}\frpc.exe') or not PlainDestination(App + '\bundled\{#AppVersion}\frps.exe') or not PlainDestination(App + '\bundled\{#AppVersion}\LICENSE') or not PlainDestination(App + '\bundled\{#AppVersion}\manifest.json') then begin
    Result := ExpandConstant('{cm:LinkedPath}'); Exit;
  end;
  if FileExists(App + '\bundled') or FileExists(App + '\bundled\{#AppVersion}') or DirExists(App + '\bundled\{#AppVersion}\frpc.exe') or DirExists(App + '\bundled\{#AppVersion}\frps.exe') or DirExists(App + '\bundled\{#AppVersion}\LICENSE') or DirExists(App + '\bundled\{#AppVersion}\manifest.json') then begin
    Result := ExpandConstant('{cm:UnmanagedDir}'); Exit;
  end;
  if DirExists(App + '\bundled\{#AppVersion}') and not FileExists(Exe) and not FileExists(App + '\native-install.json') then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
  if (DirExists(App + '\data') or FileExists(Exe)) and not FileExists(App + '\native-install.json') then begin
    Result := ExpandConstant('{cm:UnmanagedDir}'); Exit;
  end;
  Registered := RegQueryStringValue(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1', 'InstallLocation', RegisteredDir);
  if Registered then begin
    if CompareText(RemoveBackslash(RegisteredDir), RemoveBackslash(App)) <> 0 then begin
      Result := ExpandConstant('{cm:WrongInstallDir}'); Exit;
    end;
  end else begin
    if RegKeyExists(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1') then begin
      Result := ExpandConstant('{cm:UnregisteredExe}'); Exit;
    end;
  end;
  Reinstall := not Registered and not EmptyDirectory(App);
  if Reinstall and not FileExists(App + '\native-install.json') then begin
    Result := ExpandConstant('{cm:UnregisteredExe}'); Exit;
  end;
  if not SafeTempVolume(ExpandConstant('{tmp}')) then begin
    Result := ExpandConstant('{cm:UnsafeTemp}'); Exit;
  end;
  if IsArm64 then HelperName := 'maintenance-arm64.exe' else HelperName := 'maintenance-amd64.exe';
  ExtractTemporaryFile(HelperName);
  Helper := ExpandConstant('{tmp}\') + HelperName;
  if Reinstall then begin
    if not RunHelper('--native-maintenance preflight-existing') then begin
      Result := ExpandConstant('{cm:UnsafeTarget}'); Exit;
    end;
  end else if not RunHelper('--native-maintenance preflight') then begin
    Result := ExpandConstant('{cm:UnsafeTarget}'); Exit;
  end;
  if Registered or Reinstall then begin
    if DirExists(App + '\bundled\{#AppVersion}') then begin
      if Registered then begin
        if not RegQueryStringValue(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1', 'DisplayVersion', PreviousVersion) or (PreviousVersion <> '{#AppVersion}') then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
      end;
      if IsArm64 then ExpectedManifest := '{#BundledARM64SHA}' else ExpectedManifest := '{#BundledAMD64SHA}';
      try
        if Lowercase(GetSHA256OfFile(App + '\bundled\{#AppVersion}\manifest.json')) <> ExpectedManifest then begin
          Result := ExpandConstant('{cm:BundledMismatch}'); Exit;
        end;
      except
        Result := ExpandConstant('{cm:UnmanagedDir}'); Exit;
      end;
      if not RunHelper('--verify-bundled') then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
    end;
    if not RunHelper('--native-maintenance stop') then Result := ExpandConstant('{cm:StopFailed}');
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    if not RunHelper('--verify-bundled') then RaiseException('Bundled core verification failed; account data was retained.');
    if not RunHelper('--native-maintenance init') then RaiseException(ExpandConstant('{cm:InitFailed}'));
  end;
  if (CurStep = ssDone) and WizardSilent then Log(ExpandConstant('{cm:ManualStart}'));
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
  if not UninstallSilent then Result := MsgBox(ExpandConstant('{cm:UninstallConfirm}'), mbConfirmation, MB_YESNO) = IDYES;
end;
