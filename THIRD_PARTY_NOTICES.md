# Third-Party Notices

## WMPFDebugger

`miniapp-bridge` is a Go port of evi0s/WMPFDebugger at commit
`2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`.

- Original project: https://github.com/evi0s/WMPFDebugger
- Original author: evi0s
- License: GPL-2.0-only

The address configurations and embedded Agent are derived from that fixed
reference. Protocol definitions corresponding to its `src/third-party` files
originated from WeChat DevTools and retain the Tencent Holdings Ltd. copyright.

## Frida

The Windows package contains `frida-core` 17.3.2 from the official Frida
devkit. Its C API is exposed to Go through the project-owned opaque shim. See
the Frida distribution and source repository for its complete notices:
https://github.com/frida/frida

## zlib

Windows builds statically link zlib 1.3.1.

Copyright (C) 1995-2024 Jean-loup Gailly and Mark Adler.

The complete zlib license is preserved at
`third_party/zlib/src-1.3.1/LICENSE`.
