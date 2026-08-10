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
devkit. Its C API is exposed to Go through the project-owned opaque shim.

Frida 17.3.2 is distributed under the wxWindows Library Licence 3.1 in
[`licenses/frida-17.3.2/COPYING`](licenses/frida-17.3.2/COPYING). That text
references the GNU Library General Public License version 2, supplied in
[`licenses/frida-17.3.2/COPYING.LIB`](licenses/frida-17.3.2/COPYING.LIB).
Native and product ZIPs package these texts as `FRIDA_COPYING` and
`FRIDA_COPYING.LIB`, respectively.

- Frida source: https://github.com/frida/frida/tree/17.3.2
- Frida `COPYING` source: https://raw.githubusercontent.com/frida/frida/17.3.2/COPYING
- Frida `COPYING` SHA-256: `5EA1544B51A28BC823B03159190D4108F9FB4F4EF912389F5137C6D295E175B2`
- GNU Library GPL 2.0 source: https://www.gnu.org/licenses/old-licenses/lgpl-2.0.txt
- `COPYING.LIB` SHA-256: `CC535C21133C895B56B374C8A1DC1EB948D99003ED2B47372069456B62F42B24`

## zlib

The Windows Release DLL statically links zlib 1.3.1. The Go Module and final
executable do not link a system zlib DLL or import library.

Source URL: https://zlib.net/fossils/zlib-1.3.1.tar.gz
Source archive SHA-256: `9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23`

The archive and extracted source are build caches under
`third_party/downloads/cache/` and `third_party/zlib/src-1.3.1/`; they are
ignored by git and are verified before use. The first uncached Windows build
requires network access to the pinned URL or a legally obtained,
hash-matching cache. A verified cache allows subsequent `-Offline` builds.

Copyright (C) 1995-2024 Jean-loup Gailly and Mark Adler.

The complete zlib license is supplied by the downloaded source at
`third_party/zlib/src-1.3.1/LICENSE` and must be retained with distributed
source/binary packages as required by the zlib license.

## Distribution boundary

The Go module intentionally contains Go source and the minimal loader ABI only.
The Frida DLL, generated zlib library, and downloaded devkits are ignored build
artifacts and are not redistributed through the module. Release archives are
created by `scripts/native-release.ps1`; consumers verify and install them with
`scripts/native-prepare.ps1` or `sdk.PrepareNativeRuntime`. Keep this notice,
the GPL-2.0-only project license, both pinned Frida license texts, and the zlib
license beside any packaged binary.
