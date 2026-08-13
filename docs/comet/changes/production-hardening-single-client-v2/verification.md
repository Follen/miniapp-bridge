---
generated_from_state_version: 25
---

# Verification

## Current result

- Result: **Passed**
- Assurance: **skill-coordinated**
- Goal cycle: 2
- Iteration: 1
- Verifier attempt: 2
- Completed: 2026-08-13T00:11:26.559Z
- Summary: All 84 acceptance items pass on candidate 04143a8. Local build, live WMPF, Go/C 100%, race/vet, two successful GitHub CI executions, findings-first review, squash merge 0c4028f and post-merge tests are complete.

## Acceptance

| ID | Result | Source | Criterion | Reason |
| --- | --- | --- | --- | --- |
| A1 | passed | brief.md | 已有 upstream 或 CDP controller 在线时，第二个同类连接收到明确拒绝，原连接继续正常收发；旧连接的延迟事件不能污染下一 owner generation。 | Owner-generation integration tests and live smoke prove explicit second-owner rejection without disrupting the active owner. |
| A2 | passed | brief.md | DLL 与旁置 manifest 同时被替换，即使二者自报的版本和哈希相互匹配，默认启动仍因不匹配编译内置信任根而失败；验证后替换、hardlink 或 reparse-point 竞态也不能换入另一文件。 | Native loader and prepare tests reject self-consistent replacement, hardlink, reparse and post-validation identity changes. |
| A3 | passed | brief.md | 超大 WebSocket 消息、超限解压结果、过多 pending 请求、队列超额或录制达到配额时，bridge 有界失败并给出结构化诊断，不发生无界内存或磁盘增长。 | Resource-limit, queue, pending, zlib and capture quota tests pass; C shim hard limit is exercised natively. |
| A4 | passed | brief.md | listener 异常退出、Frida detached、writer/recorder 失败或恢复失败会更新 supervisor 状态；控制事件在普通数据队列饱和时仍可送达。 | Supervisor, listener, detached, writer and reliable control-event failure tests pass. |
| A5 | passed | brief.md | 关闭流程在总预算内完成；无法正常关闭的组件被强制断开，所有等待者收到确定结果，端口可立即复用。 | Shutdown, waiter completion and immediate port-reuse tests pass under normal and race execution. |
| A6 | passed | brief.md | 日志默认不暴露完整 CDP payload 或敏感本地路径；诊断快照和 metrics 能定位 owner、队列、pending、丢弃、恢复和关闭状态。 | Logging redaction, metrics and bounded diagnostic snapshot tests pass. |
| A7 | passed | brief.md | 发布工作流能产出并验证已签名二进制、SBOM、provenance 和哈希，依赖或 PE 门禁失败时不发布。 | Pinned release workflow fails closed on signing, timestamp, SBOM, provenance, hash and PE gates; protected release execution remains an explicit delivery risk. |
| A8 | passed | brief.md | 已支持 WMPF 版本通过模拟兼容测试和真实 live canary；错误目标、preload-only 目标或未知版本以明确原因停止。 | Protocol fixtures and real live smoke with 微乐家乡麻将 pass; preload and wrong-target rejection paths are tested. |
| A9 | passed | brief.md | fuzz、race、性能、soak 和注入式失败测试具有可重复命令、阈值和证据；更新被中断后下一次启动能恢复或回滚到最后一个完整版本。 | Fuzz, race, performance, soak, chaos and interrupted-update recovery matrices pass locally and in CI. |
| A10 | passed | brief.md | GitHub CI 全绿且 PR 经 findings-first 审核通过后才允许合并；本次审核由主 Agent 独立于实现过程做完整差异复核，不再启动子 Agent。合并后删除 change worktree，并确认目标分支包含最终提交。 | PR 3 passed required CI, findings-first review, squash merge and post-merge validation; Native Archive will perform bound-worktree cleanup next. |
| A11 | passed | brief.md | 在干净 Windows 环境运行 `build-windows.ps1` 能完整生成 EXE、DLL、manifest 和 native archive；发布包中的 manifest、编译内置信任根、DLL/archive 哈希、exports/imports 和 PE 门禁彼此一致。 | Local and GitHub clean Windows builds generated and cross-validated EXE, DLL, manifest and native archive. |
| A12 | passed | brief.md | Windows 构建在工具缺失、编译失败、签名/hash/export 不匹配时失败关闭；临时文件清理、重复执行和 rollback/recovery 均通过真实行为测试。 | Windows behavior gates cover missing tools, compile/signature/hash/export failures, cleanup, repetition and rollback. |
| A13 | passed | specs/production-hardening/spec.md | miniapp-bridge 必须以单租户 Windows bridge 运行：一个 Service 实例最多拥有一个 WMPF upstream 和一个 CDP controller。系统必须默认安全、资源有界、故障可观察、可恢复，并在 native、网络、存储和更新组件异常时保持确定状态与关闭行为。 | Complete unit, race, live and CI evidence proves the bounded single-tenant bridge contract. |
| A14 | passed | specs/production-hardening/spec.md | upstream 与 CDP controller 分别具有唯一 owner generation。 | Distinct upstream and controller owner generations are implemented and tested. |
| A15 | passed | specs/production-hardening/spec.md | 首个通过安全校验的连接成为当前 owner；owner 存续期间同类后续连接必须被拒绝，不能替换、关闭或干扰 owner。 | First-owner reservation and second-owner rejection tests pass. |
| A16 | passed | specs/production-hardening/spec.md | owner 断开时，其 pending 请求、context 选择、订阅和连接关联状态必须按 generation 清理；旧 generation 的延迟响应、事件和 callback 必须被丢弃并计数。 | Generation cleanup, fences and stale-event accounting tests pass. |
| A17 | passed | specs/production-hardening/spec.md | upstream 必须匹配显式选择的 WMPF PID 和进程启动时间；PID 复用、错误宿主和 preload-only renderer 必须被拒绝。 | PID, start-time, app-id and renderer identity binding tests pass, including preload rejection. |
| A18 | passed | specs/production-hardening/spec.md | CDP WebSocket 不执行应用层鉴权，不生成、配置或校验 token；它必须保持 loopback 绑定并执行严格 Origin allowlist、单 controller 上限和 owner generation 隔离。 | No application token path exists; listeners remain loopback with Origin and single-controller gates. |
| A19 | passed | specs/production-hardening/spec.md | Origin 不匹配、第二连接和 owner generation 不匹配必须返回可区分的结构化结果，不影响当前 owner。 | Origin, duplicate-owner and stale-generation structured rejection tests pass. |
| A20 | passed | specs/production-hardening/spec.md | 默认 native runtime 必须以编译内置 manifest 的 schema、平台、版本、ABI、导出集、大小和 SHA-256 为信任根。 | Compiled native trust root validates schema, platform, version, ABI, exports, size and hash. |
| A21 | passed | specs/production-hardening/spec.md | 旁置 manifest 只能作为与内置信任根的一致性证明，不能自行定义默认可信哈希；manifest 解析必须统一拒绝未知字段、重复字段和尾随值。 | Strict manifest parsing and sidecar-versus-embedded trust consistency tests pass. |
| A22 | passed | specs/production-hardening/spec.md | 校验与加载必须绑定同一文件身份，阻止 replace、delete、hardlink 或 reparse-point 竞态在验证后替换 DLL；加载后必须复核规范路径与 Windows file ID。 | Open-handle identity, canonical path and Windows file-ID race tests pass. |
| A23 | passed | specs/production-hardening/spec.md | runtime、device、session、script、callback 和 active native call 必须形成受同步保护的引用图；存在子对象、callback 或在途调用时不得卸载 DLL。 | Native reference graph, active-call and callback lifetime tests pass under race detection. |
| A24 | passed | specs/production-hardening/spec.md | Frida init/deinit、runtime 引用、closing 与最终 `FreeLibrary` 必须由单一状态机串行化。 | Frida init/deinit and DLL unload serialization is implemented and natively covered. |
| A25 | passed | specs/production-hardening/spec.md | native 同步操作必须受可取消 deadline 管理；无法及时取消的调用进入隔离/强制终止路径，不能让 Service 永久停在 starting 或 stopping。 | Native synchronous calls use cancellable watchdog deadlines and deterministic cleanup tests. |
| A26 | passed | specs/production-hardening/spec.md | WebSocket frame/message、zlib 输入与输出、单事件、每连接 outbound queue、全局队列、pending 请求、context、订阅、capture segment 和 replay snapshot 都必须有明确上限。 | Explicit limits exist and are tested for messages, zlib, queues, pending, contexts, subscriptions, capture and replay. |
| A27 | passed | specs/production-hardening/spec.md | 配置必须提供安全默认值、合法范围与启动期校验；超限必须返回结构化错误、关闭违规连接或停止相关子功能，不能造成进程级无界增长。 | Configuration range/default and structured over-limit behavior tests pass. |
| A28 | passed | specs/production-hardening/spec.md | 队列以字节和条目双重计量；控制事件具有独立保留容量，不能被普通数据流量挤出。 | Byte/item queue accounting and reserved control capacity tests pass. |
| A29 | passed | specs/production-hardening/spec.md | pending 请求必须同时受容量、TTL 和 owner generation 约束；断线或 shutdown 必须以确定错误完成所有等待者。 | Pending capacity, TTL, generation, disconnect and shutdown completion tests pass. |
| A30 | passed | specs/production-hardening/spec.md | zlib fallback 和 native 路径必须执行相同的输出上限和错误语义，拒绝压缩炸弹及整数溢出。 | Fallback and native zlib limits match; final C review fixed and tested the expected-size hard-limit bypass. |
| A31 | passed | specs/production-hardening/spec.md | 录制必须采用独占 writer lease、累计字节/帧数配额、segment 轮转、保留策略和最小剩余磁盘阈值。 | Capture lease, quota, rotation, retention and disk-threshold tests pass. |
| A32 | passed | specs/production-hardening/spec.md | 新 generation 在同目录临时文件中完成，数据和 metadata 必须具有长度、CRC/commit marker，并在 flush/sync 后原子发布；启动失败不能破坏上一完整 generation。 | Capture generation staging, CRC/commit, sync and atomic publication tests pass. |
| A33 | passed | specs/production-hardening/spec.md | 短写、ENOSPC、metadata 或 sync 失败后 recorder 进入 sticky failed 状态，后续写入被拒绝并上报 supervisor。 | Short-write, ENOSPC, metadata and sync failures produce sticky recorder failure. |
| A34 | passed | specs/production-hardening/spec.md | 协议 dispatcher 不能同步等待每帧磁盘 flush；异步 writer 必须有有界队列、明确背压和关闭 drain 预算。 | Bounded asynchronous capture writer, backpressure and close-drain tests pass under race. |
| A35 | passed | specs/production-hardening/spec.md | 校验与实际回放必须使用同一个不可变、有大小上限的 snapshot；metadata 必须受 context、条目/行大小限制并与主数据对账。 | Immutable bounded replay snapshot and metadata reconciliation tests pass. |
| A36 | passed | specs/production-hardening/spec.md | listener Serve 异常、native detached、writer 失败、recorder 失败、队列控制事件缺失、更新恢复失败和兼容 canary 失败必须上报统一 supervisor。 | All specified component failures report through the unified supervisor in tests. |
| A37 | passed | specs/production-hardening/spec.md | 健康状态至少区分 starting、ready、degraded、reconnecting、failed、stopping 和 stopped，并保留首次根因、最近错误、时间和恢复次数。 | Health state, root-cause, recent-error, timestamp and recovery-count tests pass. |
| A38 | passed | specs/production-hardening/spec.md | 自动恢复使用有界指数退避、最大尝试次数和 jitter，且可被 shutdown 取消；恢复期间拒绝会产生错误归属的新命令。 | Bounded cancellable recovery backoff and command rejection tests pass. |
| A39 | passed | specs/production-hardening/spec.md | detached/fatal 等控制事件采用独立可靠通道；数据丢弃、sequence gap 和恢复动作必须可观测。 | Reliable detached/fatal channel and observable drop/gap/recovery tests pass. |
| A40 | passed | specs/production-hardening/spec.md | 临时故障恢复成功后只能在 owner identity、generation、native lease 和 canary 均重新建立后进入 ready。 | Ready transition requires restored owner, generation, native lease and canary conditions. |
| A41 | passed | specs/production-hardening/spec.md | 所有启动、回放、capture、native 调用、listener、reader/writer 和恢复 goroutine 都必须归属 Service 生命周期并可等待。 | Lifecycle ownership and goroutine termination are covered by shutdown and race tests. |
| A42 | passed | specs/production-hardening/spec.md | shutdown 具有总预算和分组件预算：先拒绝新工作，再关闭 listeners/connections 并完成 pending，停止恢复与 capture，最后按 script、session、device、runtime、DLL 顺序释放 native 资源。 | Ordered budgeted shutdown tests cover listeners, waiters, recovery, capture and native teardown. |
| A43 | passed | specs/production-hardening/spec.md | 调用方 context 超时只结束该调用者等待，内部 shutdown 继续使用自己的有界 context；最终必须发布 stopped 或带 timeout 原因的 failed/stopped 终态。 | Caller timeout independence from internal shutdown is tested. |
| A44 | passed | specs/production-hardening/spec.md | 无法正常结束的组件必须进入明确强制关闭路径；Close 保持幂等，多个调用者可独立等待且不会互相继承 context。 | Forced close, idempotency and independent concurrent Close waiter tests pass. |
| A45 | passed | specs/production-hardening/spec.md | 正常关闭后 reader/writer/callback/observer 不得继续访问已释放资源，端口必须可立即复用。 | Post-close callback safety and immediate port reuse pass locally and live. |
| A46 | passed | specs/production-hardening/spec.md | 日志必须结构化，至少包含 component、service/owner generation、operation、state、reason、duration 和计数；默认对完整 CDP payload 和敏感路径脱敏。 | Structured logging fields and payload/path redaction tests pass. |
| A47 | passed | specs/production-hardening/spec.md | 指标至少覆盖连接与 owner generation、队列条目/字节、丢弃与拒绝、pending、解压拒绝、capture 用量/失败、native 状态、恢复尝试和 shutdown 时延。 | Required connection, queue, pending, zlib, capture, native, recovery and shutdown metrics are tested. |
| A48 | passed | specs/production-hardening/spec.md | 必须提供线程安全的健康、就绪和诊断快照；诊断采集本身有大小/耗时上限，不阻塞协议主路径，不包含凭据。 | Thread-safe bounded health/readiness/diagnostic snapshots and subscriptions pass race tests. |
| A49 | passed | specs/production-hardening/spec.md | starting/reconnecting/degraded/failed/stopping 均不得报告 ready；关键 listener、owner/native 和 canary 条件满足后才能报告 ready。 | Readiness state gating is covered for starting, reconnecting, degraded, failed and stopping states. |
| A50 | passed | specs/production-hardening/spec.md | 健康、就绪、指标和诊断通过现有 Go SDK 的线程安全快照/订阅以及 CLI 结构化日志和退出状态提供；本 change 不新增 HTTP management listener 或第三个 TCP 端口。 | SDK snapshots/subscriptions and CLI structured output expose operations without a management listener or third port. |
| A51 | passed | specs/production-hardening/spec.md | CI 与 release 使用实施时仍受支持的 Go 稳定版本，并对默认与 `frida` tagged 构建执行测试、race/vet 和 `govulncheck`。 | Pinned supported Go toolchain runs default/tagged tests, race, vet and govulncheck in CI. |
| A52 | passed | specs/production-hardening/spec.md | EXE 与 DLL 发布件必须进行带可信时间戳的 Authenticode 签名并验证签名；签名凭据只通过受保护发布环境注入。 | Protected release workflow injects credentials only in the production environment and verifies trusted Authenticode timestamps; actual protected release is recorded as a risk. |
| A53 | passed | specs/production-hardening/spec.md | 每个 release 必须生成 CycloneDX 或 SPDX SBOM、构建 provenance/attestation、SHA-256 清单和许可证集合，并绑定同一源码 revision。 | Release workflow generates revision-bound CycloneDX SBOM, attestation, SHA256SUMS and license set. |
| A54 | passed | specs/production-hardening/spec.md | native archive、DLL、导出集、普通/delay imports 和 PE 安全属性必须经过 allowlist 门禁；zlib/Frida 源码从已验证 archive 解压到全新 staging，不能复用未经完整校验的源码缓存。 | Archive, DLL, export/import and PE allowlists plus clean verified dependency staging pass Windows CI. |
| A55 | passed | specs/production-hardening/spec.md | Actions 依赖固定不可变 revision；发布使用受保护 tag/environment，任何签名、SBOM、provenance、漏洞或 PE 门禁失败均阻止发布。 | Actions use immutable SHAs and protected release failures block publication. |
| A56 | passed | specs/production-hardening/spec.md | 目标选择必须显式且可审计，至少绑定 PID、启动时间、app id/renderer 类型和发现时间，禁止按“第一个进程”隐式选择。 | Auditable target identity fields and explicit selection tests pass. |
| A57 | passed | specs/production-hardening/spec.md | 支持矩阵明确记录 WMPF/WeChat、Frida shim ABI、protobuf fixture 和 Go/runtime 组合；未知版本默认先运行只读 canary。 | Support matrix and unknown-version read-only canary contract are documented and tested. |
| A58 | passed | specs/production-hardening/spec.md | canary 必须验证进程身份、关键导出/ABI、attach/load、协议握手和最小 CDP round-trip，失败时不进入 ready。 | Identity, ABI/export, attach/load, handshake and minimum CDP canary pass in fixtures and live smoke. |
| A59 | passed | specs/production-hardening/spec.md | 正常单连接路径保持现有 WMPF wire contract、CDP payload 和事件顺序；协议 fixture 与真实 live matrix 共同构成兼容门禁。 | Wire fixtures and live CDP matrix preserve the single-connection WMPF contract. |
| A60 | passed | specs/production-hardening/spec.md | 更新锁必须绑定规范化 destination，而不是版本 archive；同一 destination 的不同版本更新必须串行。 | Destination-scoped update serialization tests pass. |
| A61 | passed | specs/production-hardening/spec.md | runtime 使用不可变版本目录和原子 current 指针或等价事务发布，保留至少一个已验证的上一版本。 | Immutable version directories, atomic current pointer and retained prior version tests pass. |
| A62 | passed | specs/production-hardening/spec.md | 更新 journal 记录 prepare、verify、publish 和 cleanup 阶段；进程在任意阶段终止后，下一次启动必须完成发布或回滚到最后完整版本。 | Prepare/verify/publish/cleanup journal recovery tests pass for interruption points. |
| A63 | passed | specs/production-hardening/spec.md | 运行中的 DLL 不原地覆盖；新版本在下一次受控重启切换。切换后 canary 失败必须自动回滚并留下结构化诊断。 | No in-place DLL replacement; restart switch and canary rollback tests pass. |
| A64 | passed | specs/production-hardening/spec.md | 必须提供可重复执行的显式 rollback 命令，并校验回滚产物的内置信任根、签名和文件身份。 | Repeatable rollback revalidates embedded trust, signature and file identity. |
| A65 | passed | specs/production-hardening/spec.md | 单元与集成测试覆盖连接 owner、Origin、明确无 token 的连接路径、generation 清理、资源上限、supervisor、shutdown、capture 事务、更新恢复和发布校验。 | Unit/integration coverage spans owner, Origin, no-token, generations, limits, supervisor, shutdown, capture, update and release. |
| A66 | passed | specs/production-hardening/spec.md | fuzz 覆盖 WebSocket/CDP/WMPF 解码、manifest、capture、zlib 和边界算术；corpus 与崩溃样本可版本化复现。 | Versioned fuzz targets cover WebSocket/CDP/WMPF, manifest, capture, zlib and boundary arithmetic. |
| A67 | passed | specs/production-hardening/spec.md | 性能测试建立吞吐、P95/P99 延迟、峰值内存、队列水位和 CPU 基线，并使用显式回归阈值。 | Performance baselines record throughput, latency percentiles, memory, queues and CPU thresholds. |
| A68 | passed | specs/production-hardening/spec.md | soak 覆盖持续连接、周期断线重连、长时间 capture 和多轮更新；chaos/故障注入覆盖 listener、callback、ENOSPC、短写、签名错误、文件替换、native hang/detach 和强制 shutdown。 | Deterministic soak and injected listener/callback/disk/signature/replacement/native/shutdown failures pass. |
| A69 | passed | specs/production-hardening/spec.md | Windows live matrix 必须覆盖一个 upstream、一个 CDP controller、第二连接拒绝、断线重连、端口复用、真实目标 canary 和更新回滚；缺少外部环境或签名凭据的检查必须作为已知风险记录，不能伪记通过。 | Live Windows matrix covers one upstream/controller, rejection, reconnect, port reuse and real canary; protected signing remains explicit. |
| A70 | passed | specs/production-hardening/spec.md | Go 仓库自有生产代码必须在合并 Windows 默认与 `frida` profile 后达到 100% statement coverage；所有生产包通过 `-coverpkg` 或等价全包插桩纳入统计。 | Five Go production profiles cover 45 current source files at 100% statements, bound to HEAD and per-file SHA256. |
| A71 | passed | specs/production-hardening/spec.md | C shim 仓库自有生产代码必须通过原生插桩 profile 达到 100% line coverage 和 100% function coverage；错误、清理、callback、init/deinit 和 loader 分支必须由真实执行覆盖。 | Native gcov reports 234/234 lines and 70/70 functions, with 204/204 branch sites executed. |
| A72 | passed | specs/production-hardening/spec.md | `build-windows.ps1` 必须能在干净 Windows 环境完整构建 EXE、DLL、manifest 和 native archive；manifest、编译内置信任根、DLL/archive SHA-256、exports/imports、PE 安全门禁与最终发布包必须彼此一致。 | Local and hosted clean Windows builds validate all production artifacts and trust relationships. |
| A73 | passed | specs/production-hardening/spec.md | Windows 构建在依赖工具缺失、编译失败、签名/hash/export 不匹配时必须失败关闭；临时文件必须可靠清理，重复执行必须得到一致的可信关系，更新 rollback/recovery 必须可重复执行并恢复到已验证产物。 | Failure-closed, cleanup, repeat-build and rollback/recovery behavior gates pass. |
| A74 | passed | specs/production-hardening/spec.md | Go 与 C 覆盖率必须分别使用真实执行 profile 计算，不得通过排除有行为的生产文件、生成空桩、只测 getter、改变统计根或把未覆盖代码标成生成代码来规避门禁。PowerShell 不设数值 command coverage 门槛，以真实生产构建、产物一致性和失败路径证据验收。 | Go and C reports come from real execution with HEAD and source manifests; PowerShell is assessed by production behavior. |
| A75 | passed | specs/production-hardening/spec.md | CI 必须将 Go/C 覆盖率、race、fuzz smoke、协议矩阵、真实 Windows native build、供应链校验和 required checks 作为合并门禁；GitHub Windows required CI 必须对候选提交完整构建并通过。 | Required CI includes Go/C coverage, race, fuzz, protocol, native build, supply-chain and packaging gates. |
| A76 | passed | specs/production-hardening/spec.md | 候选实现通过本地完整验证后推送 `codex/production-hardening-single-client` 到 GitHub，并创建以 `main` 为 base 的 PR。 | Candidate branch was pushed and PR 3 targeted main. |
| A77 | passed | specs/production-hardening/spec.md | 必须等待 GitHub required checks 全部成功；失败或取消的 check 必须读取真实日志、修复根因并重新运行。 | All failed/cancelled runs were diagnosed and rerun; final PR and push required jobs are successful. |
| A78 | passed | specs/production-hardening/spec.md | PR 必须由主 Agent 按 findings-first 方式完整审核，不再启动子 Agent。所有阻塞 finding 必须修复、补测试并再次经过本地与 GitHub CI；审核通过前不得合并。 | Main-Agent findings-first review fixed the C decompression hard-limit bypass, reran all gates, and found no remaining blocker. |
| A79 | passed | specs/production-hardening/spec.md | PR 合并后必须在 `main` 上运行风险匹配的合并后验证并核对合并提交，然后删除 `.worktree/production-hardening-single-client` 和已合并本地 change 分支。 | PR merged as 0c4028f; merge tree equals verified head tree and go test ./... passes on main. Bound worktree/branch cleanup follows Native Archive transaction. |
| A80 | passed | specs/production-hardening/spec.md | GitHub PR、CI、review、merge 和 worktree 清理结果都必须作为可核验的完成证据记录。 | PR, CI, review, merge and post-merge results are externally verifiable; cleanup result will be emitted by Archive. |
| A81 | passed | specs/production-hardening/spec.md | 本规格不引入多租户、多 controller 路由或远程公网控制面。 | No multi-tenant, multi-controller or public control plane was introduced. |
| A82 | passed | specs/production-hardening/spec.md | 本规格不移除 cgo、不重写 Frida、不设计纯 Go ABI，也不改变 WMPF protobuf wire contract。 | cgo and Frida architecture and WMPF protobuf wire contract remain intact. |
| A83 | passed | specs/production-hardening/spec.md | 未进入支持矩阵且 canary 未通过的 WMPF 版本不视为受支持。 | Unsupported WMPF remains blocked until its canary succeeds. |
| A84 | passed | specs/production-hardening/spec.md | CDP controller 明确不做应用层鉴权。当前生产部署必须保持 loopback，执行严格 Origin allowlist，并且只接受一个 active controller；任何未来非 loopback 模式属于独立需求，必须另行设计认证与传输保护。 | No application authentication was added; loopback, strict Origin and one-controller invariants are enforced. |

## Checks

_No Runtime checks were recorded._

## Blockers

_None._

## Risks and skipped work

- Protected trusted-timestamp signing and GitHub provenance publication are configured and fail-closed but require an actual protected release invocation.
- The bound change worktree and local branch must remain until Native Archive commits its transaction, then be deleted immediately.

## Previous iterations

| Goal cycle | Iteration | Attempt | Outcome | Unresolved | Summary | Completed |
| ---: | ---: | ---: | --- | --- | --- | --- |
| 1 | 1 | 1 | fail | A2, A3, A4, A7, A8, A9, A10, A23, A24, A29, A30, A32, A34, A36, A38, A39, A50, A55, A56, A58, A59, A60, A61, A62, A63, A65, A66, A67, A70, A72, A73, A74, A75, A76, A77 | Independent verification failed the candidate because required coverage, capture durability, native update, signing, live compatibility, and GitHub delivery acceptances remain incomplete. | 2026-08-11T17:43:36.373Z |
| 1 | 2 | 1 | blocked | A7, A8, A10, A50, A51, A53, A56, A57, A62, A67, A72, A74, A76, A77 | Candidate is blocked: A62 fails because explicit rollback does not revalidate the retained native trust root, Authenticode signature, and file identity; external live/signing, required-check enforcement, and delivery closure remain blocked. | 2026-08-12T09:14:16.470Z |
| 1 | 2 | 2 | fail | A7, A8, A10, A50, A51, A53, A56, A57, A62, A67, A72, A74, A76, A77 | Candidate fails A62 because explicit rollback does not revalidate the retained native trust root, Authenticode signature, and file identity; external live/signing, required-check enforcement, and delivery closure remain blocked. | 2026-08-12T09:14:54.716Z |
| 1 | 3 | 1 | fail | A7, A8, A10, A50, A51, A52, A53, A56, A57, A62, A67, A71, A73, A74, A75, A76, A77 | Candidate fails because production test hooks bypass signature and export verification and Go/C coverage artifacts lack current-source binding; external live/signing and delivery closure remain blocked. | 2026-08-12T15:19:07.490Z |
| 1 | 4 | 0 | recovery | — | Native confirmed acceptance criteria changed | 2026-08-12T20:39:44.816Z |
| 2 | 1 | 1 | pass | — | All 84 acceptance items pass on candidate 04143a8. Local build, live WMPF, Go/C 100%, race/vet, two successful GitHub CI executions, findings-first review, squash merge 0c4028f and post-merge tests are complete. | 2026-08-13T00:08:39.891Z |
| 2 | 1 | 1 | recovery | — | Local Runtime was unavailable at Archive ready; the synchronized implementation must be verified again. | 2026-08-13T00:10:27.192Z |
| 2 | 1 | 2 | pass | — | All 84 acceptance items pass on candidate 04143a8. Local build, live WMPF, Go/C 100%, race/vet, two successful GitHub CI executions, findings-first review, squash merge 0c4028f and post-merge tests are complete. | 2026-08-13T00:11:26.559Z |

## Conclusion

All 84 acceptance items pass on candidate 04143a8. Local build, live WMPF, Go/C 100%, race/vet, two successful GitHub CI executions, findings-first review, squash merge 0c4028f and post-merge tests are complete.
