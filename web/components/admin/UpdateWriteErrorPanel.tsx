"use client";

import type { ReactNode } from "react";
import type { UpdateCheckInfo } from "@/lib/types";

export type { UpdateCheckInfo };

export function UpdateWriteErrorPanel({ info }: { info: UpdateCheckInfo }) {
  const cat = info.write_err_category || "other";
  const dir = info.binary_path ? info.binary_path.substring(0, info.binary_path.lastIndexOf("/")) : "";
  const binName = info.binary_path ? info.binary_path.substring(info.binary_path.lastIndexOf("/") + 1) : "gokych";

  let title = "写入失败";
  let diagnosis: ReactNode = null;
  let solutions: ReactNode[] = [];

  if (cat === "erofs") {
    title = "🔒 只读文件系统（read-only file system）";
    diagnosis = (
      <>
        <p>文件权限 <code>{info.dir_permissions || "0777"}</code> 没有问题——<strong>chmod / chown 无法解决这个问题</strong>。
        写入失败是因为该目录所在的文件系统被<strong>挂载为只读</strong>，操作系统从内核层面拒绝了所有写操作。</p>
        {info.mount_options && info.mount_options.includes("ro") && (
          <p>检测到挂载选项包含 <code>ro</code>（read-only）：<code>{info.mount_options}</code></p>
        )}
      </>
    );
    if (info.in_container) {
      solutions = [
        <>这是<strong>容器环境</strong>：二进制在镜像层中本身就是只读的。自更新功能不适用于容器部署——请通过重建/拉取新镜像来更新，或将二进制放在可写 volume 挂载路径下。</>,
      ];
    } else {
      solutions = [
        <>
          <strong>检查 systemd 服务配置</strong>（最常见原因）：运行 <code className="err-chip">systemctl cat {binName}</code>，查看是否有 <code>ProtectSystem=strict</code>、<code>ProtectSystem=full</code>、<code>ReadOnlyPaths=-/opt</code>、<code>ReadOnlyPaths={dir}</code> 等指令。这些会将 <code>/opt</code> 以只读方式挂载到服务命名空间中。修复方法：在服务文件中添加 <code>ReadWritePaths={dir}</code> 然后 <code>systemctl daemon-reload && systemctl restart {binName}</code>。
        </>,
        <>
          <strong>检查 /etc/fstab</strong>：运行 <code className="err-chip">mount | grep '{dir}'</code> 或 <code>findmnt {dir}</code> 查看挂载选项是否包含 <code>ro</code>。
        </>,
        <>
          <strong>检查内核日志</strong>：运行 <code className="err-chip">dmesg | tail -30</code>，如果看到 "remounted read-only" 或 EXT4/XFS error，说明磁盘有 I/O 错误导致内核自动保护，先修复磁盘问题。
        </>,
      ];
    }
  } else if (cat === "eacces") {
    title = "🚫 权限不足（permission denied）";
    diagnosis = (
      <p>进程用户 <code>{info.process_user}</code> 对目录 <code>{dir}</code>（权限 <code>{info.dir_permissions}</code>）没有写入权限。</p>
    );
    solutions = [
      <>修改目录权限：<code className="err-chip">chmod 775 {dir}</code></>,
      <>修改目录所有者：<code className="err-chip">chown {info.process_user || "$USER"} {dir}</code></>,
      <>如使用 systemd：确保服务 <code>User=</code> 与目录所有者一致。</>,
    ];
  } else if (cat === "eperm") {
    title = "⛔ 操作被拒绝（operation not permitted）";
    diagnosis = (
      <p>操作系统安全模块拒绝了写入操作，这通常不是普通文件权限问题。</p>
    );
    solutions = [
      <>检查文件不可变标志：<code className="err-chip">lsattr {dir}/{binName}</code>，如果有 <code>i</code> 标志（immutable），用 <code>chattr -i {dir}/{binName}</code> 解除。</>,
      <>检查 SELinux/AppArmor 状态：<code className="err-chip">getenforce</code> 或 <code>aa-status</code>。</>,
      <>macOS 上检查 SIP（系统完整性保护）：二进制路径如果在受 SIP 保护的目录（如 <code>/System</code>、<code>/usr</code>）下，即使是 root 也无法写入。</>,
    ];
  } else {
    diagnosis = <p>错误信息：<code>{info.can_write_error}</code></p>;
    solutions = [
      <>检查磁盘空间是否充足（<code>df -h {dir}</code>）。</>,
      <>检查目录是否存在且可访问（<code>ls -la {dir}</code>）。</>,
    ];
  }

  return (
    <div className="write-error-panel">
      <div className="write-error-title">{title}</div>
      {diagnosis}
      {info.can_write_error && cat !== "other" && (
        <div className="write-error-sysmsg">
          系统错误: {info.can_write_error}
        </div>
      )}
      {solutions.length > 0 && (
        <div className="write-error-solutions">
          <div className="write-error-solutions-title">🔧 解决方法：</div>
          <ul className="write-error-list">
            {solutions.map((s, i) => <li key={i}>{s}</li>)}
          </ul>
        </div>
      )}
      {(info.process_user || info.dir_permissions) && (
        <div className="write-error-meta">
          {info.process_user && <>进程用户: <code>{info.process_user}</code>{"　"}</>}
          {info.dir_permissions && <>目录权限: <code>{info.dir_permissions}</code>{"　"}</>}
          {info.mount_options && <>挂载选项: <code>{info.mount_options}</code></>}
        </div>
      )}
    </div>
  );
}