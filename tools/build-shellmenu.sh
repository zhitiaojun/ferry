#!/usr/bin/env bash
# Build the Explorer context-menu DLL, the stub host and the sparse MSIX payload.
# Windows + llvm-mingw; outputs go to .tools/dist (not committed).
set -euo pipefail
cd "$(dirname "$0")/.."
MINGW=${MINGW:-.tools/llvm-mingw/llvm-mingw-20261104-ucrt-x86_64/bin}
export TMP="$(cygpath -w "$PWD/.tools/tmp")"
export TEMP="$TMP"
export PATH="$PWD/$MINGW:$PATH"
export GOPATH="$(cygpath -w "$PWD/.tools/gopath")"
export GOCACHE="$(cygpath -w "$PWD/.tools/gocache")"
mkdir -p .tools/dist

clang --target=x86_64-w64-mingw32 -shared -O2 -o .tools/dist/ftmenu.dll \
  cmd/ft-shellmenu/ftmenu.c cmd/ft-shellmenu/ftmenu.def \
  -Wl,--exclude-all-symbols -lole32 -luuid -lshell32 -ladvapi32
clang --target=x86_64-w64-mingw32 -municode -O2 -o .tools/dist/ftmenu_test.exe \
  cmd/ft-shellmenu/ftmenu_test.c -lole32 -luuid -lshell32 -ladvapi32
.tools/go/bin/go.exe build -o .tools/dist/ftstub.exe ./cmd/ft-msix-stub

# stage the package next to the installer payload
mkdir -p cmd/ft-setup/payload/menu/Assets
cp .tools/dist/ftmenu.dll cmd/ft-setup/payload/menu/
cp .tools/dist/ftstub.exe cmd/ft-setup/payload/menu/
cp deploy/msix/AppxManifest.xml cmd/ft-setup/payload/menu/
cp deploy/msix/Assets/*.png cmd/ft-setup/payload/menu/Assets/

.tools/go/bin/go.exe build -o dist/windows/ft-setup.exe ./cmd/ft-setup
echo "built: dist/windows/ft-setup.exe"
