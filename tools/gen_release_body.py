#!/usr/bin/env python3
"""Generate concise Bettbox-style GitHub Release notes with direct download badges."""

from __future__ import annotations

import argparse
import pathlib
import re


def extract_changelog(changelog_path: pathlib.Path, tag: str, repo: str) -> str:
    """Extract bullet lines for the matching version heading in CHANGELOG.md."""
    ver = tag.lstrip("v")
    fallback = (
        f"- 轻渡 Ferry `{tag}` 版本发布，完整变更请查看 "
        f"[CHANGELOG.md](https://github.com/{repo}/blob/main/CHANGELOG.md)"
    )
    if not changelog_path.is_file():
        return fallback

    lines = changelog_path.read_text(encoding="utf-8").splitlines()
    # Match "## 1.0.1", "## 1.0.1 — ...", or "## 1.0.1 - ..."
    heading_re = re.compile(rf"^##\s+{re.escape(ver)}(?:\s+[-—].*)?$")

    collecting = False
    collected: list[str] = []
    for line in lines:
        if not collecting:
            if heading_re.match(line.strip()):
                collecting = True
            continue
        if line.startswith("## "):
            break
        if line.strip():
            collected.append(line.rstrip())

    return "\n".join(collected) if collected else fallback


def format_mb(assets_dir: pathlib.Path, filename: str) -> str:
    """Return formatted MiB string if the asset file exists, else empty string."""
    p = assets_dir / filename
    if not p.is_file():
        return ""
    return f"{p.stat().st_size / (1024 * 1024):.2f} MB"


def build_body(tag: str, repo: str, changelog_path: pathlib.Path, assets_dir: pathlib.Path) -> str:
    notes = extract_changelog(changelog_path, tag, repo)
    title_kind = "测试版本发布" if "-" in tag else "正式版本发布"
    base_dl = f"https://github.com/{repo}/releases/download/{tag}"

    win_sz = format_mb(assets_dir, "ferry-setup-windows-amd64.exe")
    arm64_sz = format_mb(assets_dir, "ferry-android-arm64.apk")
    armv7_sz = format_mb(assets_dir, "ferry-android-armv7a.apk")
    cli_sz = format_mb(assets_dir, "ferry-linux-amd64")
    srv_sz = format_mb(assets_dir, "ferry-server-linux-amd64")

    win_note = "含桌面端、CLI 与右键菜单" + (f"，{win_sz}" if win_sz else "")

    apk_sizes: list[str] = []
    if arm64_sz:
        apk_sizes.append(f"ARMv8 {arm64_sz}")
    if armv7_sz:
        apk_sizes.append(f"ARMv7 {armv7_sz}")
    android_note = "现代机型选 ARMv8，老旧 32 位机型选 ARMv7" + (
        f" · {' · '.join(apk_sizes)}" if apk_sizes else ""
    )

    linux_parts: list[str] = []
    linux_parts.append(f"CLI / MCP {cli_sz}" if cli_sz else "CLI / MCP")
    linux_parts.append(f"自建服务端 {srv_sz}" if srv_sz else "自建服务端")
    linux_note = " · ".join(linux_parts)

    android_badges = (
        f'<a href="{base_dl}/ferry-android-arm64.apk">'
        f'<img src="https://img.shields.io/badge/APK-ARMv8-32AF6A?logo=android&logoColor=white&style=flat-square&labelColor=222222">'
        f"</a> "
        f'<a href="{base_dl}/ferry-android-armv7a.apk">'
        f'<img src="https://img.shields.io/badge/APK-ARMv7-32AF6A?logo=android&logoColor=white&style=flat-square&labelColor=222222">'
        f"</a>"
    )
    linux_badges = (
        f'<a href="{base_dl}/ferry-linux-amd64">'
        f'<img src="https://img.shields.io/badge/CLI-Linux%20x64-F84E29?logo=linux&logoColor=white&style=flat-square&labelColor=222222">'
        f"</a> "
        f'<a href="{base_dl}/ferry-server-linux-amd64">'
        f'<img src="https://img.shields.io/badge/Server-Linux%20x64-555555?logo=linux&logoColor=white&style=flat-square&labelColor=222222">'
        f"</a>"
    )
    win_badge = (
        f'<a href="{base_dl}/ferry-setup-windows-amd64.exe">'
        f'<img src="https://img.shields.io/badge/Setup-Windows%20x64-0078D7?logo=windows&logoColor=white&style=flat-square&labelColor=222222">'
        f"</a>"
    )

    return f"""{notes}

# 轻渡 Ferry {tag} {title_kind}

### ⬇️ 下载直达

| **系统** | **版本要求 / 说明** | **点击直链下载** |
|:---:|:---|:---|
| <img src="https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/windows11/windows11-original.svg" alt="Windows" width="28"/> | Windows 10 / 11 (x64)<br>*({win_note})* | {win_badge} |
| <img src="https://cdn.jsdelivr.net/gh/devicons/devicon/icons/android/android-original.svg" alt="Android" width="28"/> | Android 5.0+<br>*({android_note})* | {android_badges} |
| <img src="https://cdn.jsdelivr.net/gh/devicons/devicon/icons/linux/linux-original.svg" alt="Linux" width="28"/> | Linux (x86_64)<br>*({linux_note})* | {linux_badges} |

> 校验文件：[SHA256SUMS.txt]({base_dl}/SHA256SUMS.txt) · Windows 首次运行如遇 SmartScreen 提示，点击「更多信息 → 仍要运行」即可。

---

### 🐛 问题反馈

遇到问题或有功能建议，欢迎携带复现步骤与日志提交：[GitHub Issues](https://github.com/{repo}/issues)

> 本项目由 [阿里云边缘安全加速 ESA](https://www.aliyun.com/product/esa) 提供边缘加速、计算与安全保护 · 开源协议：[AGPL-3.0](https://github.com/{repo}/blob/main/LICENSE)
"""


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate Ferry GitHub Release Markdown body.")
    parser.add_argument("--tag", required=True, help="Git tag, e.g. v1.0.1")
    parser.add_argument("--repo", default="zhitiaojun/ferry", help="owner/repo slug")
    parser.add_argument("--changelog", default="CHANGELOG.md", help="Path to CHANGELOG.md")
    parser.add_argument("--assets-dir", required=True, help="Directory with built release assets")
    parser.add_argument("--out", required=True, help="Output markdown path")
    args = parser.parse_args()

    body = build_body(
        tag=args.tag,
        repo=args.repo,
        changelog_path=pathlib.Path(args.changelog),
        assets_dir=pathlib.Path(args.assets_dir),
    )
    out_path = pathlib.Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(body, encoding="utf-8", newline="\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
