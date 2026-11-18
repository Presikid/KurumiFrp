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
SetupMutex=Global\io.github.Presikid.KurumiFRP.Setup
AppName=KurumiFRP
AppVersion={#AppVersion}
AppPublisher=Presikid
AppPublisherURL=https://github.com/Presikid/KurumiFrp
DefaultDirName={commonpf}\KurumiFRP
UsePreviousAppDir=yes
DisableDirPage=no
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible or arm64
ArchitecturesInstallIn64BitMode=x64compatible or arm64
MinVersion=6.1sp1
WizardStyle=modern
WizardImageFile=wizard.bmp
WizardSmallImageFile=wizard-small.bmp
SetupIconFile=kurumi.ico
UninstallDisplayName=KurumiFRP
UninstallDisplayIcon={app}\kurumi.ico
OutputDir={#OutputPath}
OutputBaseFilename=KurumiFRP-{#AppVersion}-Setup
Compression=lzma2
SolidCompression=yes

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
chinesesimplified.DesktopTask=创建桌面快捷方式（双击在浏览器打开 WebUI）
english.DesktopTask=Create a desktop shortcut (opens WebUI in browser)
chinesesimplified.AutostartTask=开机自动在后台静默启动 KurumiFRP 服务
english.AutostartTask=Start KurumiFRP background service silently at sign-in
chinesesimplified.AskCleanPurge=是否同时执行「清洁卸载」，彻底删除软件安装目录内的所有配置文件 (data) 与运行数据？
english.AskCleanPurge=Perform a Clean Uninstall and remove all configuration files (data) inside the installation folder?
chinesesimplified.StopFailed=无法确认当前安装的 KurumiFRP 已停止。请关闭该实例后重试。
english.StopFailed=Setup could not confirm that this KurumiFRP instance has stopped. Please close it and retry.

[Tasks]
Name: desktopicon; Description: "{cm:DesktopTask}"
Name: autostart; Description: "{cm:AutostartTask}"; Flags: unchecked

[Dirs]
Name: "{app}\data"; Permissions: users-modify
Name: "{app}\data\configs"; Permissions: users-modify
Name: "{app}\data\runtime"; Permissions: users-modify

[Files]
Source: "{#PayloadDir}\amd64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: not IsArm64
Source: "{#PayloadDir}\amd64\bundled\*"; DestDir: "{app}\bundled"; Flags: ignoreversion recursesubdirs createallsubdirs; Check: not IsArm64
Source: "{#PayloadDir}\arm64\kurumi.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: IsArm64
Source: "{#PayloadDir}\arm64\bundled\*"; DestDir: "{app}\bundled"; Flags: ignoreversion recursesubdirs createallsubdirs; Check: IsArm64
Source: "{#PayloadDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "kurumi.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\KurumiFRP WebUI"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"; IconFilename: "{app}\kurumi.ico"
Name: "{group}\卸载 KurumiFRP"; Filename: "{uninstallexe}"
Name: "{commondesktop}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--open"; WorkingDir: "{app}"; IconFilename: "{app}\kurumi.ico"; Tasks: desktopicon
Name: "{commonstartup}\KurumiFRP"; Filename: "{app}\kurumi.exe"; Parameters: "--headless"; WorkingDir: "{app}"; Tasks: autostart

[Run]
Filename: "{app}\kurumi.exe"; Parameters: "--native-maintenance init --install-dir ""{app}"""; Flags: runhidden waituntilterminated
Filename: "{app}\kurumi.exe"; Parameters: "--open"; Description: "启动 KurumiFRP 并在浏览器打开时崎狂三 WebUI"; Flags: nowait postinstall skipifsilent runasoriginaluser
Filename: "{app}\kurumi.exe"; Parameters: "--headless --listen ""{param:LISTENADDR|auto}"" {code:DataArgs}"; WorkingDir: "{app}"; Flags: nowait runhidden runasoriginaluser; Check: ShouldRestartHeadless

[Code]
function ShouldRestartHeadless: Boolean;
begin
  Result := ExpandConstant('{param:RESTARTDAEMON|0}') = '1';
end;

function DataArgs(Param: String): String;
var
  DataDir: String;
begin
  DataDir := ExpandConstant('{param:DATADIR|}');
  if DataDir = '' then
    Result := ''
  else
    Result := '--data "' + AddBackslash(DataDir) + '."';
end;

function WaitForExecutable(ExePath: String): Boolean;
var
  Attempt: Integer;
  Probe: TFileStream;
begin
  { The listener can close before the daemon finishes stopping its FRP children.
    Another instance of this executable may also use a different data directory. }
  Result := False;
  for Attempt := 1 to 60 do begin
    try
      Probe := TFileStream.Create(ExePath, fmOpenReadWrite or fmShareDenyNone);
      Probe.Free;
      Result := True;
      Exit;
    except
      Sleep(250);
    end;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  ExePath: String;
begin
  Result := '';
  ExePath := ExpandConstant('{app}\kurumi.exe');
  if FileExists(ExePath) then begin
    if not ExecAsOriginalUser(ExePath, '--stop ' + DataArgs(''), ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode)
        or (ResultCode <> 0) or not WaitForExecutable(ExePath) then
      Result := ExpandConstant('{cm:StopFailed}');
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  AppDir, ExePath: String;
begin
  AppDir := ExpandConstant('{app}');
  ExePath := AppDir + '\kurumi.exe';
  if CurUninstallStep = usUninstall then begin
    if FileExists(ExePath) then begin
      if not Exec(ExePath, '--stop ' + DataArgs(''), AppDir, SW_HIDE, ewWaitUntilTerminated, ResultCode)
          or (ResultCode <> 0) or not WaitForExecutable(ExePath) then begin
        SuppressibleMsgBox(ExpandConstant('{cm:StopFailed}'), mbError, MB_OK, IDOK);
        Abort;
      end;
    end;
  end else if CurUninstallStep = usPostUninstall then begin
    if (not UninstallSilent) and (MsgBox(ExpandConstant('{cm:AskCleanPurge}'), mbConfirmation, MB_YESNO) = IDYES) then begin
      DelTree(AppDir + '\data', True, True, True);
      DelTree(AppDir + '\runtime', True, True, True);
      DeleteFile(AppDir + '\native-install.json');
      RemoveDir(AppDir);
    end;
  end;
end;
