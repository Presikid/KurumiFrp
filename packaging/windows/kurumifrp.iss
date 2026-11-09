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
UsePreviousAppDir=no
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

[CustomMessages]
english.ShortcutGroup=Shortcuts:
english.StartupGroup=Startup:
english.CleanupGroup=Installer cleanup:
english.DesktopTask=Create a desktop shortcut
english.AutostartTask=Start a private workbench when each user signs in
english.CleanupTask=Delete this Setup file after successful installation
english.OpenApp=Open KurumiFRP
chinesesimplified.ShortcutGroup=快捷方式：
chinesesimplified.StartupGroup=启动选项：
chinesesimplified.CleanupGroup=安装包清理：
chinesesimplified.DesktopTask=创建桌面快捷方式
chinesesimplified.AutostartTask=用户登录时启动各自的工作台
chinesesimplified.CleanupTask=安装成功后删除本次安装程序
chinesesimplified.OpenApp=打开 KurumiFRP

english.LegacyInstall=A legacy per-user installation is present. Back up its data and cores and remove it before installing the machine-wide edition. No legacy data was changed.
chinesesimplified.LegacyInstall=检测到旧版用户级安装。请先备份配置与核心并卸载旧版，再安装当前版本。旧数据未被修改。
english.LinkedPath=Linked installation directories are not supported. No files were changed.
chinesesimplified.LinkedPath=不支持链接或快捷方式指向的安装目录。未修改任何文件。
english.UnmanagedDir=This directory contains an old script installation or unmanaged files. Choose a new directory. See INSTALL.md for migration.
chinesesimplified.UnmanagedDir=此目录包含旧版安装或未受管理的文件。请选择新目录，迁移方法请参阅 INSTALL.md。
english.UnregisteredExe=An executable exists here but no machine installation is registered. Choose a clean directory.
chinesesimplified.UnregisteredExe=此处已有程序文件，但未发现已注册的机器级安装。请选择干净的目录。
english.WrongInstallDir=The existing executable is not in the registered machine installation. No executable was run.
chinesesimplified.WrongInstallDir=现有程序不在已注册的安装目录中。未运行任何程序。
english.StopFailed=The running workbench could not be stopped safely. Existing data was retained.
chinesesimplified.StopFailed=无法安全停止正在运行的工作台。现有数据已保留。
english.InitFailed=Installation receipt could not be initialized. Setup.exe was retained.
chinesesimplified.InitFailed=无法初始化安装记录，安装程序已保留。
english.StartFailed=Could not start KurumiFRP.
chinesesimplified.StartFailed=无法启动 KurumiFRP。
english.ReadinessFailed=The local API did not become ready. Setup.exe was retained; existing configuration was not deleted.
chinesesimplified.ReadinessFailed=本机服务未能就绪。安装程序已保留，现有配置未被删除。
english.BundledMismatch=This same-version installer contains a different bundled core. Install a newer application version instead; the current installation was not changed.
chinesesimplified.BundledMismatch=此安装包与已安装的同版本程序所含预置核心不同。请安装更新的软件版本；现有安装未被修改。
english.CleanupFailed=Setup finished, but its downloaded EXE could not be removed. You may delete that EXE manually.
chinesesimplified.CleanupFailed=安装已完成，但无法删除下载的安装程序。你可以稍后手动删除。
english.UninstallConfirm=Uninstall KurumiFRP for all users and permanently delete all accounts configurations, authentication keys, downloaded frp cores and application logs?
chinesesimplified.UninstallConfirm=是否为所有用户卸载 KurumiFRP，并永久删除各账户的配置、密钥、已下载核心和应用记录？
english.PurgeFailed=Shutdown or data removal was not confirmed. Uninstall stopped; retry after resolving the error. No process was killed by name.
chinesesimplified.PurgeFailed=未能确认程序已停止或数据已删除。卸载已中止，请排除问题后重试。

[Tasks]
Name: desktopicon; Description: "{cm:DesktopTask}"; GroupDescription: "{cm:ShortcutGroup}"
Name: autostart; Description: "{cm:AutostartTask}"; GroupDescription: "{cm:StartupGroup}"; Flags: unchecked
Name: cleanup; Description: "{cm:CleanupTask}"; GroupDescription: "{cm:CleanupGroup}"

[Files]
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

[Run]
Filename: "{app}\kurumi.exe"; Parameters: "--open"; Description: "{cm:OpenApp}"; Flags: postinstall nowait skipifsilent

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
  if not Result then MsgBox(ExpandConstant('{cm:LegacyInstall}'), mbError, MB_OK);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var Code: Integer; App, Exe, RegisteredDir, PreviousVersion, ExpectedManifest: String;
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
  if DirExists(App + '\bundled\{#AppVersion}') and not FileExists(Exe) then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
  if (DirExists(App + '\data') or FileExists(Exe)) and not FileExists(App + '\native-install.json') then begin
    Result := ExpandConstant('{cm:UnmanagedDir}'); Exit;
  end;
  if FileExists(Exe) then begin
    if not RegQueryStringValue(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1', 'InstallLocation', RegisteredDir) then begin
      Result := ExpandConstant('{cm:UnregisteredExe}'); Exit;
    end;
    if CompareText(RemoveBackslash(RegisteredDir), RemoveBackslash(App)) <> 0 then begin
      Result := ExpandConstant('{cm:WrongInstallDir}'); Exit;
    end;
    if DirExists(App + '\bundled\{#AppVersion}') then begin
      if not RegQueryStringValue(HKLM, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\io.github.Presikid.KurumiFRP_is1', 'DisplayVersion', PreviousVersion) or (PreviousVersion <> '{#AppVersion}') then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
      if not Exec(Exe, '--verify-bundled', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
      if Code <> 0 then begin Result := ExpandConstant('{cm:UnmanagedDir}'); Exit; end;
      if IsArm64 then ExpectedManifest := '{#BundledARM64SHA}' else ExpectedManifest := '{#BundledAMD64SHA}';
      try
        if Lowercase(GetSHA256OfFile(App + '\bundled\{#AppVersion}\manifest.json')) <> ExpectedManifest then begin
          Result := ExpandConstant('{cm:BundledMismatch}'); Exit;
        end;
      except
        Result := ExpandConstant('{cm:UnmanagedDir}'); Exit;
      end;
    end;
    if not Exec(Exe, '--native-maintenance stop', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then Result := ExpandConstant('{cm:StopFailed}');
  end;
end;

procedure RemoveLegacyTranslationNotice();
var P, D: String;
begin
  D := ExpandConstant('{app}\licenses');
  P := D + '\ChineseSimplified-LICENSE.txt';
  if FileExists(P) and PlainDestination(P) then begin
    try
      if Lowercase(GetSHA256OfFile(P)) = '02f0dd3b98833040147d01daf91243c24392a33ddced674152220b5216782c6a' then begin
        if DeleteFile(P) then RemoveDir(D);
      end;
    except
      Log('Legacy translation notice could not be removed; it was retained.');
    end;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var Code: Integer; App, Exe: String;
begin
  if CurStep = ssPostInstall then begin
    RemoveLegacyTranslationNotice();
    App := ExpandConstant('{app}'); Exe := App + '\kurumi.exe';
    if not Exec(Exe, '--verify-bundled', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException('Bundled core verification failed; existing files were retained.');
    if not Exec(Exe, '--native-maintenance init', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException(ExpandConstant('{cm:InitFailed}'));
    if not ExecAsOriginalUser(Exe, '', App, SW_HIDE, ewNoWait, Code) then RaiseException(ExpandConstant('{cm:StartFailed}'));
    if not ExecAsOriginalUser(Exe, '--check-running', App, SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException(ExpandConstant('{cm:ReadinessFailed}'));
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
      MsgBox(ExpandConstant('{cm:CleanupFailed}'), mbInformation, MB_OK);
  end;
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
  if not UninstallSilent then Result := MsgBox(ExpandConstant('{cm:UninstallConfirm}'), mbConfirmation, MB_YESNO) = IDYES;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var Code: Integer;
begin
  if CurUninstallStep = usUninstall then begin
    if not Exec(ExpandConstant('{app}\kurumi.exe'), '--native-maintenance purge', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, Code) then Code := -1;
    if Code <> 0 then RaiseException(ExpandConstant('{cm:PurgeFailed}'));
  end;
end;
