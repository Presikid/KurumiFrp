# KurumiFRP 0.5.0 系统目录安装预览版

- Windows 默认安装到 Program Files，UAC 管理员授权，HKLM 注册，公共快捷方式；不再默认装入用户 AppData。
- 可变文件集中在 ProgramData/KurumiFRP，按 Windows SID 隔离配置、认证密钥和 frp 核心。普通用户日常运行，不给 Users 放开 Program Files 写权限。
- 安装、升级和卸载覆盖机器级安装及全部产品账户目录；保留准确路径/SHA-256 验证的 Setup 自清理。
- TMP/TEMP/TMPDIR 统一到本次运行专用目录，托管 frp 同步继承；浏览器不继承此重定向。
- Linux 保持 DEB/RPM、/usr 程序和 /var/lib 状态分工，增加 /run/kurumifrp 的 systemd 临时目录及退出回收。
- 新增 --paths 用于查询实际数据、核心与临时目录。
- 原生 CI 增加真实普通 Windows 账户、不同账户私密文件拒绝访问、只读程序目录和实际子进程临时写入检查。

Windows 从 0.4 当前用户版需要先备份并移除旧安装，不能自动无确认接管其他账户数据。Linux 0.4 原生包可覆盖升级。仍为未签名预览包；ARM64 未做硬件验收，不能承诺系统和浏览器零记录。详见 docs/INSTALL.md。
