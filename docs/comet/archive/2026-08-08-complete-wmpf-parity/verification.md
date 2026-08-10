# Acceptance evidence

<!-- historical-acceptance-summary:start -->
The public archive keeps the historical acceptance IDs and statuses. Raw machine receipts, snapshots, and developer-specific runtime metadata are intentionally excluded; the command summaries below are the retained audit record.

| Acceptance ID | Historical status |
| --- | --- |
| `acceptance-57ca7c9d743817f8abe630b2c6877d87eac38087b67b22896dd99d836c762eed` | passed |
| `acceptance-5a08b12a267314c48da379aad3c1f7d706b9d9e0af8776c348bbfef2285a7913` | passed |
| `acceptance-8fc22df3cdf7b059bdf074c158b79eebdeca763ce0fe3f0cd5c29b5e3ec056ba` | passed |
| `acceptance-b0f8901c7a7bd76470106a6537cbf7e24560956e7e85f7df8462349b7dff7eee` | passed |
| `acceptance-cded77aeed14a7b3ddf6cfe5bfa819b52f9fc4cec72ea935d9b6347359e51c98` | passed |
| `acceptance-ea88e62bd0e2c7996e0eec9b16495d7826fb1677cf90fce295fb876a41321ebb` | passed |
<!-- historical-acceptance-summary:end -->

# Commands and results

- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/coverage-gate.ps1`: exit 0. Default and Frida-tagged `internal/...` production profiles both reported `total: (statements) 100.0%`; smoke process runner reported `100.0%`; unit, race, tagged race, and vet passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1`: exit 0. zlib 1.3.1 and Frida core 17.3.2 hashes matched; tagged race passed; EXE SHA-256 `EBE8D6994801E019442BF9CA14E7C154DB2223D5C807D558191FA13A915E6E72`; DLL SHA-256 `EBBA08E33735094D597CEC1E2A978D98D9B790FAA3BE56570746777A1848967A`.
- `go test ./internal/wmpf ./internal/app -count=1 -timeout 180s`: exit 0. Reference protobuf/Codex differential fixtures, corrupt inputs, context/request routing, simulated multi-client CDP matrix, and connection-lifecycle shutdown synchronization passed.
- `go test ./scripts -run Zlib -count=1 -v`: exit 0. Fixed URL/SHA contract, verified offline cache build, missing-cache failure, and temporary-file cleanup passed.
- `powershell.exe ... scripts/smoke-windows.ps1 -UpstreamWaitSeconds 300`: exit 0 on Windows WMPF 25297. The bridge owned 9421/62000; Frida attached PID 43432; post-attach OnLoadStart fired; peer PID 36284 passed exact four-tuple and `PID@StartTime` revalidation on attempt 2. Live CDP returned 7 domains, 10 initializers, objects/exceptions/console/scripts/pause-resume/callframes, 491520-byte payload, 16 concurrent requests, structured error, contexts, reconnect, `events=625`, `receive-order=true`, and `order-assertions=44`. Link smoke passed. Renderer PID 35372, attached host, and upstream peer survived graceful shutdown; transient renderer PID 15264 exited as expected; ports were released.
- Windows PowerShell 5.1 AST parsing for `coverage-gate.ps1`, `build-windows.ps1`, and `smoke-windows.ps1`: exit 0 with no parse errors.

# Skipped checks

None for the confirmed acceptance matrix. The zlib 1.3.1 source is acquired from the pinned URL `https://zlib.net/fossils/zlib-1.3.1.tar.gz`, verified against SHA-256 `9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23`, and kept in ignored download/extraction caches. No product behavior or acceptance command is skipped.

# Spec consistency

The implementation keeps the fixed reference ports, listener-before-attach startup order, all reference address configurations, 55 protobuf types, reference Codex mappings, request/event/context semantics, embedded Agent behavior, and Frida 17.3.2 ownership lifecycle. Link smoke remains identified as transport-only evidence; the separate live matrix provides the full CDP evidence required by the specification.

# Known limitations and risks

The final live process evidence was obtained on installed WMPF 25297. All other address files from the fixed reference repository are selected and audited by automated tests, but each requires its corresponding installed WMPF runtime for an additional live hook run. Windows is the first supported platform. Non-Windows zlib remains wire-compatible but is not claimed byte-identical until a pinned native zlib package is added for that platform. The strict Go statement gate covers the specified production scope under `internal/...` plus the smoke process runner; CLI entry wrappers and verification tooling are exercised by unit/contract/live tests rather than included in that production profile. The Comet verification scope excludes ignored third-party download/extraction caches from source inspection; the build and SHA-256 checks remain part of the acceptance evidence.

# Conclusion

Pass. All six confirmed acceptance items were recorded as passed for revision 34 and complete scope `b29b38ed3bf3904f84dfa9076a831dfc9bcc6a74897a5ba58b8390f2cf1d2ade`, including native build, 100.0% scoped production statement coverage, reference differential fixtures, simulated end-to-end CDP, zlib offline behavior, and real Windows live CDP with graceful shutdown and target survival.
