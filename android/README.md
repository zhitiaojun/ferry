# `android/`

轻渡 Ferry Android 原生客户端（包名：`cn.cwlindean.ferry`），采用原生 Java UI（Material Design 3）+ `gomobile` 编译 Go 核心（`./core/mobile`），支持 Android 5.0+（`arm64-v8a`，16 KB ELF 页对齐），无 Gradle 依赖。

## 本地构建依赖

- Go（版本见根目录 `go.mod`）与 `gomobile` / `gobind`
- JDK 17
- Android SDK `platforms;android-34`、`build-tools;34.0.0`
- Android NDK r27c（`27.2.12479018`）
- Python 3（用于向 APK 追加 `classes.dex` 与 `libgojni.so`）

## 构建命令

```bash
# 通过环境变量指定版本与签名密钥，或使用脚本默认本地测试配置
VER_NAME=1.0.0 ./android/build.sh
```

完整 CI 构建与发布签名流程见 [`.github/workflows/release.yml`](../.github/workflows/release.yml)。
