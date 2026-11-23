#!/usr/bin/env bash
# Build the minimal M2 Android APK without Gradle.
#
#   ./android/build.sh
#
# Output: android/build/ft-debug.apk (arm64-v8a only).
# Requires the local toolchain under .tools/ (JDK 17, Android SDK 34,
# NDK r27c, Go, gomobile). Native tools get Windows-style paths (cygpath -m).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TOOLS=$ROOT/.tools
JDK=$TOOLS/jdk/jdk-17.0.21+9
SDK=$TOOLS/android-sdk
BT=$SDK/build-tools/34.0.0
AJAR=$SDK/platforms/android-34/android.jar
OUT=$ROOT/android/build
WORK=$TOOLS/android-build

# Windows path (forward slashes) for native tools: javac, java, aapt2, zip...
m() { cygpath -m "$1"; }

export GOCACHE=$(m "$TOOLS/gocache") GOPATH=$(m "$TOOLS/gopath") GOTMPDIR=$(m "$TOOLS/tmp") GOFLAGS=-buildvcs=false
export ANDROID_HOME=$(m "$SDK") ANDROID_NDK_HOME=$(m "$SDK/ndk/27.2.12479018") JAVA_HOME=$(m "$JDK")
export TMP=$(m "$TOOLS/tmp") TEMP=$(m "$TOOLS/tmp")
export PATH="$TOOLS/go/bin:$TOOLS/gopath/bin:$JDK/bin:$PATH"

echo "== go build =="
(cd "$ROOT" && go build ./...)

echo "== gomobile bind =="
mkdir -p "$OUT"
(cd "$ROOT" && CGO_LDFLAGS="${CGO_LDFLAGS:+$CGO_LDFLAGS }-Wl,-z,max-page-size=16384 -Wl,-z,common-page-size=16384" gomobile.exe bind -target=android/arm64 -androidapi 21 -o android/ft.aar ./core/mobile)

echo "== extract AAR =="
rm -rf "$WORK"
mkdir -p "$WORK/aar" "$WORK/classes" "$WORK/dex"
python - "$(m "$ROOT/android/ft.aar")" "$(m "$WORK/aar")" "$(m "$WORK")" <<'PY'
import sys, zipfile
aar, aarout, work = sys.argv[1], sys.argv[2], sys.argv[3]
with zipfile.ZipFile(aar) as z:
    z.extract("classes.jar", aarout)
    z.extract("jni/arm64-v8a/libgojni.so", work)
PY
mkdir -p "$WORK/lib/arm64-v8a"
cp "$WORK/jni/arm64-v8a/libgojni.so" "$WORK/lib/arm64-v8a/libgojni.so"

echo "== javac =="
cd "$ROOT"
find android/src -name '*.java' >"$WORK/sources.txt"
"$JDK/bin/javac" -encoding UTF-8 --release 8 -Xlint:-options \
  -classpath "$(m "$AJAR");$(m "$WORK/aar/classes.jar")" \
  -d "$(m "$WORK/classes")" @"$(m "$WORK/sources.txt")"

echo "== d8 (dex) =="
find .tools/android-build/classes -name '*.class' >"$WORK/classes.txt"
"$JDK/bin/java" -cp "$(m "$BT/lib/d8.jar")" com.android.tools.r8.D8 \
  --min-api 21 --lib "$(m "$AJAR")" \
  --output "$(m "$WORK/dex")" @"$(m "$WORK/classes.txt")" "$(m "$WORK/aar/classes.jar")"

echo "== aapt2 compile (res) =="
"$BT/aapt2.exe" compile --dir "$(m "$ROOT/android/res")" -o "$(m "$WORK/res.zip")"

echo "== aapt2 link =="
"$BT/aapt2.exe" link -o "$(m "$WORK/base.apk")" \
  --manifest "$(m "$ROOT/android/AndroidManifest.xml")" \
  -I "$(m "$AJAR")" --min-sdk-version 21 --target-sdk-version 34 \
  --version-code 1000 --version-name 1.0.0 "$(m "$WORK/res.zip")"

echo "== package =="
cp "$WORK/dex/classes.dex" "$WORK/classes.dex"
(cd "$WORK" && python "$ROOT/android/zipadd.py" base.apk classes.dex=classes.dex lib/arm64-v8a/libgojni.so=lib/arm64-v8a/libgojni.so)

echo "== zipalign =="
"$BT/zipalign.exe" -f -p 4 "$(m "$WORK/base.apk")" "$(m "$WORK/aligned.apk")"

echo "== key =="
if [ -f "$TOOLS/ferry-release.keystore" ] && [ -f "$TOOLS/ferry-signing.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$TOOLS/ferry-signing.env"
  set +a
  KS=$TOOLS/ferry-release.keystore
  ALIAS=${ANDROID_KEY_ALIAS:-ferry}
  KSPASS=$ANDROID_KEYSTORE_PASS
  KPASS=$ANDROID_KEY_PASS
else
  KS=$TOOLS/android-debug.keystore
  ALIAS=androiddebugkey
  KSPASS=android
  KPASS=android
  if [ ! -f "$KS" ]; then
    "$JDK/bin/keytool" -genkeypair -keystore "$(m "$KS")" -alias "$ALIAS" \
      -keyalg RSA -keysize 2048 -validity 10000 \
      -storepass "$KSPASS" -keypass "$KPASS" \
      -dname "CN=ft Debug,O=ft,C=CN" >/dev/null 2>&1
  fi
fi

echo "== sign =="
"$JDK/bin/java" -jar "$(m "$BT/lib/apksigner.jar")" sign \
  --ks "$(m "$KS")" --ks-key-alias "$ALIAS" \
  --ks-pass "pass:$KSPASS" --key-pass "pass:$KPASS" \
  --out "$(m "$OUT/ft-debug.apk")" "$(m "$WORK/aligned.apk")"

"$JDK/bin/java" -jar "$(m "$BT/lib/apksigner.jar")" verify --print-certs "$(m "$OUT/ft-debug.apk")" | head -4

echo
echo "APK: $OUT/ft-debug.apk"
ls -l "$OUT/ft-debug.apk"
