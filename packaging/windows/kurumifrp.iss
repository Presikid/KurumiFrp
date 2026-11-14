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
chinesesimplified.AutostartTask=开机自动在后台启动 KurumiFRP 服务
english.AutostartTask=Start KurumiFRP background service automatically at sign-in
chinesesimplified.AskCleanPurge=是否同时执行「清洁卸载」，彻底删除软件安装目录内的所有配置文件 (data) 与运行数据？
english.AskCleanPurge=Perform a Clean Uninstall and remove all configuration files (data) inside the installation folder?

[Tasks]
Name: desktopicon; Description: "{cm:DesktopTask}"
Name: autostart; Description: "{cm:AutostartTask}"; Flags: unchecked

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
Name: "{commonstartup}\KurumiFRP"; Filename: "{app}\kurumi.exe"; WorkingDir: "{app}"; Tasks: autostart

[Run]
Filename: "{app}\kurumi.exe"; Parameters: "--native-maintenance init --install-dir ""{app}"""; Flags: runhidden waituntilterminated
Filename: "{app}\kurumi.exe"; Parameters: "--open"; Description: "启动 KurumiFRP 并在浏览器打开时崎狂三 WebUI"; Flags: nowait postinstall skipifsilent

[Code]
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  ExePath: String;
begin
  Result := '';
  ExePath := ExpandConstant('{app}\kurumi.exe');
  if FileExists(ExePath) then
    Exec(ExePath, '--stop', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  AppDir, ExePath: String;
begin
  AppDir := ExpandConstant('{app}');
  ExePath := AppDir + '\kurumi.exe';
  if CurUninstallStep = usUninstall then begin
    if FileExists(ExePath) then
      Exec(ExePath, '--stop', AppDir, SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end else if CurUninstallStep = usPostUninstall then begin
    if WizardSilent or (SuppressibleMsgBox(ExpandConstant('{cm:AskCleanPurge}'), mbConfirmation, MB_YESNO, IDYES) = IDYES) then begin
      DelTree(AppDir + '\data', True, True, True);
      DelTree(AppDir + '\runtime', True, True, True);
      DeleteFile(AppDir + '\native-install.json');
      RemoveDir(AppDir);
    end;
  end;
end;
