# 生产级单租户 Bridge 规格

## 目标与运行模型

miniapp-bridge 必须以单租户 Windows bridge 运行：一个 Service 实例最多拥有一个 WMPF upstream 和一个 CDP controller。系统必须默认安全、资源有界、故障可观察、可恢复，并在 native、网络、存储和更新组件异常时保持确定状态与关闭行为。

## 连接所有权与端点边界

- upstream 与 CDP controller 分别具有唯一 owner generation。
- 首个通过安全校验的连接成为当前 owner；owner 存续期间同类后续连接必须被拒绝，不能替换、关闭或干扰 owner。
- owner 断开时，其 pending 请求、context 选择、订阅和连接关联状态必须按 generation 清理；旧 generation 的延迟响应、事件和 callback 必须被丢弃并计数。
- upstream 必须匹配显式选择的 WMPF PID 和进程启动时间；PID 复用、错误宿主和 preload-only renderer 必须被拒绝。
- CDP WebSocket 不执行应用层鉴权，不生成、配置或校验 token；它必须保持 loopback 绑定并执行严格 Origin allowlist、单 controller 上限和 owner generation 隔离。
- Origin 不匹配、第二连接和 owner generation 不匹配必须返回可区分的结构化结果，不影响当前 owner。

## Native 信任与所有权

- 默认 native runtime 必须以编译内置 manifest 的 schema、平台、版本、ABI、导出集、大小和 SHA-256 为信任根。
- 旁置 manifest 只能作为与内置信任根的一致性证明，不能自行定义默认可信哈希；manifest 解析必须统一拒绝未知字段、重复字段和尾随值。
- 校验与加载必须绑定同一文件身份，阻止 replace、delete、hardlink 或 reparse-point 竞态在验证后替换 DLL；加载后必须复核规范路径与 Windows file ID。
- runtime、device、session、script、callback 和 active native call 必须形成受同步保护的引用图；存在子对象、callback 或在途调用时不得卸载 DLL。
- Frida init/deinit、runtime 引用、closing 与最终 `FreeLibrary` 必须由单一状态机串行化。
- native 同步操作必须受可取消 deadline 管理；无法及时取消的调用进入隔离/强制终止路径，不能让 Service 永久停在 starting 或 stopping。

## 资源预算与背压

- WebSocket frame/message、zlib 输入与输出、单事件、每连接 outbound queue、全局队列、pending 请求、context、订阅、capture segment 和 replay snapshot 都必须有明确上限。
- 配置必须提供安全默认值、合法范围与启动期校验；超限必须返回结构化错误、关闭违规连接或停止相关子功能，不能造成进程级无界增长。
- 队列以字节和条目双重计量；控制事件具有独立保留容量，不能被普通数据流量挤出。
- pending 请求必须同时受容量、TTL 和 owner generation 约束；断线或 shutdown 必须以确定错误完成所有等待者。
- zlib fallback 和 native 路径必须执行相同的输出上限和错误语义，拒绝压缩炸弹及整数溢出。

## Capture 与回放

- 录制必须采用独占 writer lease、累计字节/帧数配额、segment 轮转、保留策略和最小剩余磁盘阈值。
- 新 generation 在同目录临时文件中完成，数据和 metadata 必须具有长度、CRC/commit marker，并在 flush/sync 后原子发布；启动失败不能破坏上一完整 generation。
- 短写、ENOSPC、metadata 或 sync 失败后 recorder 进入 sticky failed 状态，后续写入被拒绝并上报 supervisor。
- 协议 dispatcher 不能同步等待每帧磁盘 flush；异步 writer 必须有有界队列、明确背压和关闭 drain 预算。
- 校验与实际回放必须使用同一个不可变、有大小上限的 snapshot；metadata 必须受 context、条目/行大小限制并与主数据对账。

## Supervisor、健康与恢复

- listener Serve 异常、native detached、writer 失败、recorder 失败、队列控制事件缺失、更新恢复失败和兼容 canary 失败必须上报统一 supervisor。
- 健康状态至少区分 starting、ready、degraded、reconnecting、failed、stopping 和 stopped，并保留首次根因、最近错误、时间和恢复次数。
- 自动恢复使用有界指数退避、最大尝试次数和 jitter，且可被 shutdown 取消；恢复期间拒绝会产生错误归属的新命令。
- detached/fatal 等控制事件采用独立可靠通道；数据丢弃、sequence gap 和恢复动作必须可观测。
- 临时故障恢复成功后只能在 owner identity、generation、native lease 和 canary 均重新建立后进入 ready。

## 请求与关闭生命周期

- 所有启动、回放、capture、native 调用、listener、reader/writer 和恢复 goroutine 都必须归属 Service 生命周期并可等待。
- shutdown 具有总预算和分组件预算：先拒绝新工作，再关闭 listeners/connections 并完成 pending，停止恢复与 capture，最后按 script、session、device、runtime、DLL 顺序释放 native 资源。
- 调用方 context 超时只结束该调用者等待，内部 shutdown 继续使用自己的有界 context；最终必须发布 stopped 或带 timeout 原因的 failed/stopped 终态。
- 无法正常结束的组件必须进入明确强制关闭路径；Close 保持幂等，多个调用者可独立等待且不会互相继承 context。
- 正常关闭后 reader/writer/callback/observer 不得继续访问已释放资源，端口必须可立即复用。

## 可观测性与诊断

- 日志必须结构化，至少包含 component、service/owner generation、operation、state、reason、duration 和计数；默认对完整 CDP payload 和敏感路径脱敏。
- 指标至少覆盖连接与 owner generation、队列条目/字节、丢弃与拒绝、pending、解压拒绝、capture 用量/失败、native 状态、恢复尝试和 shutdown 时延。
- 必须提供线程安全的健康、就绪和诊断快照；诊断采集本身有大小/耗时上限，不阻塞协议主路径，不包含凭据。
- starting/reconnecting/degraded/failed/stopping 均不得报告 ready；关键 listener、owner/native 和 canary 条件满足后才能报告 ready。
- 健康、就绪、指标和诊断通过现有 Go SDK 的线程安全快照/订阅以及 CLI 结构化日志和退出状态提供；本 change 不新增 HTTP management listener 或第三个 TCP 端口。

## 发布供应链与工具链

- CI 与 release 使用实施时仍受支持的 Go 稳定版本，并对默认与 `frida` tagged 构建执行测试、race/vet 和 `govulncheck`。
- EXE 与 DLL 发布件不要求 Authenticode 或其他代码签名；发布包必须通过编译内置信任根、manifest、DLL/archive SHA-256、exports/imports 和 PE 安全属性门禁。
- 每个 release 必须生成 CycloneDX 或 SPDX SBOM、构建 provenance/attestation、SHA-256 清单和许可证集合，并绑定同一源码 revision。
- native archive、DLL、导出集、普通/delay imports 和 PE 安全属性必须经过 allowlist 门禁；zlib/Frida 源码从已验证 archive 解压到全新 staging，不能复用未经完整校验的源码缓存。
- Actions 依赖固定不可变 revision；发布使用受保护 tag/environment，任何 manifest/hash/SBOM/provenance、漏洞或 PE 门禁失败均阻止发布。

## 目标选择与兼容策略

- 目标选择必须显式且可审计，至少绑定 PID、启动时间、app id/renderer 类型和发现时间，禁止按“第一个进程”隐式选择。
- 支持矩阵明确记录 WMPF/WeChat、Frida shim ABI、protobuf fixture 和 Go/runtime 组合；未知版本默认先运行只读 canary。
- canary 必须验证进程身份、关键导出/ABI、attach/load、协议握手和最小 CDP round-trip，失败时不进入 ready。
- 正常单连接路径保持现有 WMPF wire contract、CDP payload 和事件顺序；协议 fixture 与真实 live matrix 共同构成兼容门禁。

## 更新事务与回滚

- 更新锁必须绑定规范化 destination，而不是版本 archive；同一 destination 的不同版本更新必须串行。
- runtime 使用不可变版本目录和原子 current 指针或等价事务发布，保留至少一个已验证的上一版本。
- 更新 journal 记录 prepare、verify、publish 和 cleanup 阶段；进程在任意阶段终止后，下一次启动必须完成发布或回滚到最后完整版本。
- 运行中的 DLL 不原地覆盖；新版本在下一次受控重启切换。切换后 canary 失败必须自动回滚并留下结构化诊断。
- 必须提供可重复执行的显式 rollback 命令，并校验回滚产物的内置信任根、manifest/hash 和文件身份。

## 测试与生产门禁

- 单元与集成测试覆盖连接 owner、Origin、明确无 token 的连接路径、generation 清理、资源上限、supervisor、shutdown、capture 事务、更新恢复和发布校验。
- fuzz 覆盖 WebSocket/CDP/WMPF 解码、manifest、capture、zlib 和边界算术；corpus 与崩溃样本可版本化复现。
- 性能测试建立吞吐、P95/P99 延迟、峰值内存、队列水位和 CPU 基线，并使用显式回归阈值。
- soak 覆盖持续连接、周期断线重连、长时间 capture 和多轮更新；chaos/故障注入覆盖 listener、callback、ENOSPC、短写、hash/manifest 错误、文件替换、native hang/detach 和强制 shutdown。
- Windows live matrix 必须覆盖一个 upstream、一个 CDP controller、第二连接拒绝、断线重连、端口复用、真实目标 canary 和更新回滚；缺少外部环境检查必须作为已知风险记录，不能伪记通过。
- Go 仓库自有生产代码必须在合并 Windows 默认与 `frida` profile 后达到 100% statement coverage；所有生产包通过 `-coverpkg` 或等价全包插桩纳入统计。
- C shim 仓库自有生产代码必须通过原生插桩 profile 达到 100% line coverage 和 100% function coverage；错误、清理、callback、init/deinit 和 loader 分支必须由真实执行覆盖。
- `build-windows.ps1` 必须能在干净 Windows 环境完整构建 EXE、DLL、manifest 和 native archive；manifest、编译内置信任根、DLL/archive SHA-256、exports/imports、PE 安全门禁与最终发布包必须彼此一致。
- Windows 构建在依赖工具缺失、编译失败、manifest/hash/export 不匹配时必须失败关闭；临时文件必须可靠清理，重复执行必须得到一致的可信关系，更新 rollback/recovery 必须可重复执行并恢复到已验证产物。
- Go 与 C 覆盖率必须分别使用真实执行 profile 计算，不得通过排除有行为的生产文件、生成空桩、只测 getter、改变统计根或把未覆盖代码标成生成代码来规避门禁。PowerShell 不设数值 command coverage 门槛，以真实生产构建、产物一致性和失败路径证据验收。
- CI 必须将 Go/C 覆盖率、race、fuzz smoke、协议矩阵、真实 Windows native build、供应链校验和 required checks 作为合并门禁；GitHub Windows required CI 必须对候选提交完整构建并通过。

## GitHub 交付与收尾

- 候选实现通过本地完整验证后推送 `codex/production-hardening-single-client` 到 GitHub，并创建以 `main` 为 base 的 PR。
- 必须等待 GitHub required checks 全部成功；失败或取消的 check 必须读取真实日志、修复根因并重新运行。
- PR 必须由主 Agent 按 findings-first 方式完整审核，不再启动子 Agent。所有阻塞 finding 必须修复、补测试并再次经过本地与 GitHub CI；审核通过前不得合并。
- PR 合并后必须在 `main` 上运行风险匹配的合并后验证并核对合并提交，然后删除 `.worktree/production-hardening-single-client` 和已合并本地 change 分支。
- GitHub PR、CI、review、merge 和 worktree 清理结果都必须作为可核验的完成证据记录。

## 非目标与兼容约束

- 本规格不引入多租户、多 controller 路由或远程公网控制面。
- 本规格不移除 cgo、不重写 Frida、不设计纯 Go ABI，也不改变 WMPF protobuf wire contract。
- 未进入支持矩阵且 canary 未通过的 WMPF 版本不视为受支持。

## CDP 端点策略

CDP controller 明确不做应用层鉴权。当前生产部署必须保持 loopback，执行严格 Origin allowlist，并且只接受一个 active controller；任何未来非 loopback 模式属于独立需求，必须另行设计认证与传输保护。
