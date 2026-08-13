# CI 与发布交付规格

## 触发与去重

CI workflow 只响应 `pull_request`、`main` 分支的 `push` 和显式 `workflow_dispatch`。功能分支 push 与 tag push 不触发 CI。同一功能提交不得因同时匹配 push 与 PR 而产生两套完整检查。并发组按 PR 编号或 main ref 隔离，只取消同一逻辑候选的旧 run，不让 tag 或其他分支取消当前门禁。

PR 是合并前唯一完整验证入口。main push 是合并后可信候选生产入口，不因 tag 创建再次运行。Branch Protection 的唯一 required context 是始终执行的 `ci-gate`；具体工作 job 可按分类新增、拆分或 skip，而不改变 required context。

## 变更分类

流水线先以 merge-base 到候选 SHA 的实际 changed paths 生成结构化分类输出。分类至少区分 workflow/docs/release-fixture 轻量变更和 Go/C/native/build/package 核心变更。分类器取不到可靠 diff、输出无效或遇到未归类生产路径时必须保守选择完整门禁。

轻量 PR 必须运行 checkout、分类、actionlint、文档契约、workflow/release/package/checksum fixture；不需要真实 native 构建、完整 coverage、race、soak 或 PE 构建时这些 job 明确为 skipped。涉及 Go、C shim、native loader、构建脚本、打包脚本、依赖锁、生产 workflow 或覆盖率门禁的改动必须运行相应完整门禁。workflow 自身变更至少运行 workflow 合约与 actionlint，并在无法证明仅影响轻量行为时选择完整门禁。

## 完整 CI 门禁

完整门禁保留模块图、gofmt、unit、vet、govulncheck、fuzz smoke、deterministic soak/chaos、race、外部 module、Go production 100% statement coverage、C shim 100% line/function coverage和生产级 Windows 行为测试。

Windows 原生发布候选只构建一次。producer job 在干净 Windows runner 上固定依赖、构建 EXE、DLL、manifest 与 native archive，执行可复现性和基础 trust-root/hash 校验，并上传绑定 source SHA 的中间 artifact。behavior、Go coverage、C coverage、PE/package 等消费者并行下载同一个 artifact；任何消费者不得再次执行完整 native build。消费者必须校验 artifact 元数据中的 source SHA 与下载内容 hash 后才使用。

Windows 行为门禁覆盖工具缺失、编译失败、hash/manifest/export/import/PE 不匹配时失败关闭，临时文件清理、重复执行、rollback/recovery 和发布包一致性。PowerShell 不设逐行或逐命令覆盖率门槛。

## 汇总门禁

`ci-gate` 使用 `if: always()` 且依赖分类器和所有候选门禁。它根据分类器声明的应运行集合逐项验证结果：应运行 job 只有 `success` 可接受；无需运行 job 只有 `success` 或 `skipped` 可接受；任何 `failure`、`cancelled`、缺失或无法识别的结果都失败。分类器自身失败或取消时 `ci-gate` 失败。

PR 与 main 均产生同名稳定 `ci-gate`。main 只有在 `ci-gate` 成功且完整发布门禁已执行时，才可发布可信候选；轻量 main 变更若会成为可发布 commit，也必须生成或继承可证明与该 source tree 一致的候选，不能把旧二进制冒充新 source SHA。

## 可信 main 候选

main 流水线为完整 commit SHA 生成一个不可混淆的发布候选 artifact。候选包含 Windows EXE/DLL、native archive、manifest、许可证/声明、构建元数据和覆盖所有 payload 的 checksum 清单。元数据记录 source SHA、workflow run、工具链/固定依赖身份与各文件 SHA-256。

候选 artifact 名称包含完整 commit SHA，保留期覆盖正常发版窗口。上传前完成 Go/C/Windows/PE/package 门禁；上传后独立下载并核对 artifact 集合、checksum 和 source SHA。门禁未通过、候选不完整或核对失败时不得标记为可晋级。

## tag Release 晋级

Release workflow 只响应完整 SemVer `v*` tag 或带显式 tag 输入的手动运行。tag push 时它验证 tag peeled commit 等于触发 SHA；手动运行以显式 tag 的 peeled commit 为准。两种入口都验证 commit 位于受保护 main 历史，并精确定位该 SHA 的成功 main CI run 和可信候选。缺失、过期、多义、来自其他 commit 或未经完整门禁的候选均失败关闭；Release 不回退到源码构建。

Release 下载可信候选后验证 artifact 元数据、source SHA、所有 SHA-256、manifest、资产集合与 native compatibility identity。之后只允许执行版本化产品打包、LF `SHA256SUMS`、SBOM、provenance、GitHub artifact attestation 和产品/native Release 的幂等发布。Release 中禁止 unit、race、fuzz、soak、coverage、native build、Go/C 编译和重复 PE 构建门禁。

发布不要求 PFX、证书、Authenticode 或 timestamp。未签名 EXE/DLL 合法；已存在但无效的签名、hash 漂移、asset 冲突、tag 移动或 release metadata 不一致仍失败关闭。发布写权限只授予最终晋级/发布 job，下载与校验使用最小只读权限。

## 性能与可观察性

轻量 PR 的工程目标为 3–5 分钟，完整 PR/main 为 10–20 分钟，tag Release 为 3–5 分钟。目标不是通过删除门禁实现，而是通过触发去重、changed-path 分类、Windows job 并行和 build artifact 复用实现。

每个 job 上传必要日志或报告，artifact 名称包含 run 或 source SHA，保留期有界。`ci-gate` 输出分类结果、每项 job 的实际状态和失败原因；Release 输出被晋级的 main run ID、source SHA、候选 artifact 名与最终资产 hash，使重复执行和故障排查可审计。
