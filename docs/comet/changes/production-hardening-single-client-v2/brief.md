# Outcome

把 miniapp-bridge 从功能完整的本地工具加固为可长期运行、可诊断、资源有界、可恢复且默认安全的 Windows 生产级单租户 bridge。每个 Service 实例严格只允许一个 WMPF upstream 和一个 CDP controller，关键后台故障必须进入可观察的健康状态，而不是仅写日志。

# Scope

- 修复 native runtime 的信任根、校验到加载之间的文件替换窗口，以及 runtime/device/session/script/active-call 的所有权与并发关闭边界。
- 将 9421 upstream 和 62000 CDP 端点收敛为严格单连接模型，拒绝第二个连接且不影响现有 owner；增加严格 Origin 策略、upstream 目标身份校验和连接代际隔离。
- 为 WebSocket 帧与消息、zlib 解压结果、事件队列、pending 请求、context/订阅、回放快照和录制文件建立明确的资源预算与失败行为。
- 建立统一 runtime supervisor、starting/ready/degraded/reconnecting/failed/stopping/stopped 状态、有界自动恢复和可取消的退避。
- 提供结构化日志、脱敏、关联字段、运行指标、健康/就绪检查和可导出的诊断快照。
- 建立有总预算和分组件预算的请求、后台任务与 shutdown 生命周期，保证强制终止后的确定终态。
- 加固发布供应链：升级到受支持 Go 工具链，加入依赖漏洞扫描、Authenticode、SBOM、provenance/attestation、PE import 与安全属性门禁。
- 建立显式 WMPF 目标选择、受支持版本策略、兼容探针和真实环境 canary，避免误连 preload 或错误宿主。
- 增加协议 differential、fuzz、性能基线、长时间 soak、并发压力、chaos 和故障注入矩阵。
- 将 native/runtime 更新改为可恢复事务：规范 destination 锁、版本化发布、启动恢复 journal、保留 N-1 并支持确定回滚。
- 分别为 Go、C shim 和 PowerShell 接入真实覆盖率工具，并把三类仓库自有生产代码都提升到数值 100%，以覆盖率门禁阻止回归。

# Non-goals

- 不实现多 upstream、多 CDP controller 的 ID 重写、响应单播或多租户上下文隔离。
- 不重写 Frida，不在本 change 中移除 cgo、设计纯 Go ABI 或替换 `miniapp-frida.dll` 架构。
- 不改变 WMPF protobuf wire contract、CDP payload 语义或已经确认的正常消息顺序。
- 不增加远程公网控制面；生产默认仍是本机 loopback bridge。
- 不承诺支持未进入兼容矩阵且 canary 未通过的 WMPF 版本。

# Acceptance examples

- 已有 upstream 或 CDP controller 在线时，第二个同类连接收到明确拒绝，原连接继续正常收发；旧连接的延迟事件不能污染下一 owner generation。
- DLL 与旁置 manifest 同时被替换，即使二者自报的版本和哈希相互匹配，默认启动仍因不匹配编译内置信任根而失败；验证后替换、hardlink 或 reparse-point 竞态也不能换入另一文件。
- 超大 WebSocket 消息、超限解压结果、过多 pending 请求、队列超额或录制达到配额时，bridge 有界失败并给出结构化诊断，不发生无界内存或磁盘增长。
- listener 异常退出、Frida detached、writer/recorder 失败或恢复失败会更新 supervisor 状态；控制事件在普通数据队列饱和时仍可送达。
- 关闭流程在总预算内完成；无法正常关闭的组件被强制断开，所有等待者收到确定结果，端口可立即复用。
- 日志默认不暴露完整 CDP payload 或敏感本地路径；诊断快照和 metrics 能定位 owner、队列、pending、丢弃、恢复和关闭状态。
- 发布工作流能产出并验证已签名二进制、SBOM、provenance 和哈希，依赖或 PE 门禁失败时不发布。
- 已支持 WMPF 版本通过模拟兼容测试和真实 live canary；错误目标、preload-only 目标或未知版本以明确原因停止。
- fuzz、race、性能、soak 和注入式失败测试具有可重复命令、阈值和证据；更新被中断后下一次启动能恢复或回滚到最后一个完整版本。
- GitHub CI 全绿且 PR 经独立子 Agent 审核通过后才允许合并；合并后删除 change worktree，并确认目标分支包含最终提交。

# Constraints and invariants

- 生产默认只绑定 loopback；任何未来非 loopback 绑定必须具有独立认证和传输保护。
- 控制事件不得因普通数据队列拥塞而静默丢失。
- 关闭顺序保持 script -> session -> device -> runtime -> DLL，且底层强制该所有权关系；存在子对象、callback 或 active call 时不得卸载 DLL。
- 正常单 upstream、单 CDP 使用路径保持兼容，现有 live CDP matrix 的协议行为不得回归。
- 所有安全边界、资源上限、恢复状态和发布门禁必须有自动化验证；环境依赖的 live 检查单独记录。
- 资源预算均提供安全默认值和有界配置，非法配置在启动时失败，运行中不得静默放宽。
- 本 change 的实现只在绑定的 `codex/production-hardening-single-client` 分支和 `.worktree/production-hardening-single-client` 工作树中进行。
- 最终发布前 Go statement、C line/function 和 PowerShell command coverage 必须分别达到 100%；不能用排除生产文件、空测试或降低统计范围制造通过结果。

# Decisions

- 连接模型为严格单租户：一个 upstream、一个 CDP controller。
- 第二个连接不接管、不替换当前连接；默认行为是拒绝新连接，保持当前会话稳定。
- 第 1-10 项生产加固全部纳入同一个 change，不拆分为“基础版”和后续批次。
- 保留现有 Go + cgo loader + `miniapp-frida.dll` 架构，本 change 不以纯 Go 构建为目标。
- upstream 连接必须绑定显式选择的 WMPF PID 与进程启动时间，防止 PID 复用或误连其他宿主。
- CDP controller 不做应用层鉴权，不生成或校验 token；安全边界为 loopback、严格 Origin、单 controller 和连接代际隔离。
- Git linked worktree 统一使用仓库内 `.worktree/<change-name>`，本 change 使用独立 worktree 隔离实现。
- 完成顺序固定为：本地完整验证 -> 推送 GitHub -> CI 全绿 -> 创建 PR -> 独立子 Agent 审核并解决全部阻塞意见 -> 合并到 `main` -> 合并后验证 -> 删除工作树和已合并本地分支。
- Go、C shim、PowerShell 的数值覆盖率必须分别达到 100%，三项都是 CI 和合并门禁。
- Go 覆盖率合并 Windows 默认与 `frida` 测试 profile；C shim 使用可复核的原生插桩 profile；PowerShell 使用脚本覆盖率 profile。
- 健康、就绪、metrics 和诊断通过现有 Go SDK 的线程安全快照/订阅及 CLI 结构化输出提供，不新增第三个管理监听端口。

# Open questions

- 无。

# Verification expectations

- 默认、race、tagged native、外部 SDK、协议 differential、模拟 CDP matrix、Windows native build 和 `govulncheck` 全部通过。
- 增加双连接拒绝、Origin、无 token 路径、owner generation、消息与解压上限、pending/录制配额、runtime 信任根、TOCTOU、native lease、Serve/detach supervisor 和关闭预算测试。
- 对 native loader/C shim 运行并发 open/close、callback drain、Application Verifier 或同等 unload-after-use 检查。
- 生成并校验 Authenticode、SBOM、provenance、PE imports/security flags 和更新事务/回滚产物。
- fuzz corpus、性能阈值、soak 时长、chaos 场景和故障注入均形成可重复的 CI 或发布前命令。
- 生成可复核的 Go、C shim 和 PowerShell 覆盖率 profile，并分别执行 100% 数值门禁；报告必须来自实际测试，不能仅依赖文件级存在性。
- 运行真实 Windows live matrix，覆盖单 upstream、单 CDP、断线、重连、优雅关闭、端口立即复用、目标 canary 和更新回滚；缺少外部签名凭据或真实环境时必须明确列为未验证风险。
- 推送后等待 GitHub required checks 完成；PR 由未参与对应实现文件的子 Agent 做 findings-first 审核，阻塞问题修复并重新验证后才能合并。
