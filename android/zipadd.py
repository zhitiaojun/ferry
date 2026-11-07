#!/usr/bin/env python3
"""Append entries to an existing APK/zip (no `zip` binary on this host).

    python zipadd.py out.apk stored.so=lib/arm64-v8a/libgojni.so deflated.dex=classes.dex
"""
import sys
import zipfile


def main(argv):
    if len(argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    apk = argv[1]
    with zipfile.ZipFile(apk, "a") as z:
        for arg in argv[2:]:
            src, _, name = arg.partition("=")
            if not name:
                name = src
            comp = zipfile.ZIP_STORED if name.endswith(".so") else zipfile.ZIP_DEFLATED
            z.write(src, name, compress_type=comp)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
