# Known Differences

The current release-candidate results and remaining environment risks are
recorded in
[`docs/comet/changes/public-go-sdk-worktree/verification.md`](docs/comet/changes/public-go-sdk-worktree/verification.md).
This file lists stable implementation differences from the fixed TypeScript
reference rather than embedding stale run output.

| Area | Current behavior | Technical reason | Production requirement |
|---|---|---|---|
| Native packaging | Windows loads `miniapp-frida.dll`, containing pinned Frida core behind the documented opaque ABI. | Windows cgo cannot consume the official MSVC static archive as a portable external Go Module dependency. The small shim keeps C/Frida handles out of Go callers. | Ship the matching Release DLL and manifest beside the final EXE and verify their hashes before load. |
| Portable zlib fallback | Untagged/non-Windows builds use Go `compress/zlib`; tagged Windows builds use stock zlib `1.3.1` in the native DLL. | The fallback preserves wire compatibility but does not promise byte-identical compressed output. | Use the tagged Windows build for byte-exact production parity. |
| Runtime reinitialization | The final native owner releases script, session, device, runtime, and DLL once; the same Service cannot restart after Close. | Frida initialization is process-wide and the pinned Windows runtime is not safely reinitialized after deinit. | Create a new process for a new live bridge lifetime, matching CLI behavior. |
| Malformed length-delimited frame | Go rejects one truncated frame that the pinned protobuf.js decoder partially accepts. | Strict rejection prevents incomplete bytes from becoming valid application data; valid frames and all other corrupt fixtures match. | Keep the differential fixture and require a diagnostic error without panic or process exit. |
| Platform scope | Windows amd64 is the first native release target. Other systems retain compile-time abstractions and portable protocol tests. | Native loader, packaging, process discovery, and live target behavior are platform-specific. | Do not claim a non-Windows native release until that platform has its own loader, packaging, and live receipts. |
| Live version breadth | All pinned reference address configurations are embedded and statically audited; a live receipt can exercise only target versions installed on the verification host. | Multiple historical WMPF binaries cannot be synthesized by unit tests. | Require a fresh live receipt for every target version declared supported by a production release. |

There are no intentional changes to the reference listener addresses, WMPF
protobuf field numbers, compression flags, CDP request/event/error semantics,
context routing, Agent hook responsibilities, or startup order.
