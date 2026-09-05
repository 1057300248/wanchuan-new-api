# 万川控制台皮肤与验收

## 交付范围

- 新增 `wanchuan` 主题预设：黑白灰明暗界面、224px 侧栏、统一间距；保留原主题及用户显式字体、圆角、密度偏好。
- 用户导航在权限过滤之后重排。模型价格、接入文档、公开体验链接使用营销站完整地址；管理员和原主题导航不重排。
- 用户概览以余额、今日消费、请求、Token 为主；接入步骤可折叠，提供接口地址复制、脱敏请求示例和最近五次成功/错误调用。
- 今日汇总和默认七日趋势共用查询。图表按本地自然日聚合小时记录，补齐无用量日期；三十日窗口跨夏令时回拨时分段查询，遵守服务端时间上限。
- 密钥详情仅在明确操作后读取完整密钥；请求示例遵循模型限制、启用状态、有效期和额度。密钥变更同步失效概览缓存，稳定列组件保留弹窗状态。
- 钱包与概览复用身份隔离的账户缓存；仅成功的新鲜响应同步余额字段，不覆盖个人资料与权限。错误状态可重试，缺失指标不伪装成零。
- 账户设置分为资料、安全、偏好；手机端可直接打开主题设置。日志与密钥列表保留原有操作和筛选。
- 手机端搜索按钮保持固定图标尺寸，避免较长翻译撑宽 320px 页头；万川预设标签在七语言中完整注册。

## 后端兼容

- 按上游 `6b659fd61c50` 移植推理强度原值保留逻辑，包含供应商适配与跨协议冲突处理。
- 管理设置的计费模式、表达式读取运行时有效配置；在配置锁内取得一致快照，锁外序列化。
- 不引入新模型默认售价，不修改管理员价格、结算规则、数据库模型、驱动、迁移或数据库读写逻辑。
- 保留 New API / QuantumNous 版权信息和所有原有项目元数据。

## 验证入口

首轮测试由 OpenCode `opencode-go/muse-spark-1.3-contributor`、`xhigh` 执行；最终修正及验收由主代理独立执行。

- 前端：受影响 Vitest 测试、TypeScript、定向 oxlint、生产构建。
- 后端：controller 有效计费配置回归，relay/helper、relay/common、相关供应商适配与 billing_setting 测试。
- 独立模块：在 `relaykit/` 设置 `GOWORK=off` 后执行测试和 `go build ./...`。
- 浏览器：七种语言、五个主要页面、明暗主题、桌面/手机；设置折叠、三十日桶、指标切换、密钥详情、键盘焦点与主题持久化。
- 性能：同一依赖和构建环境，对照 `a00381c00` 的入口资源以及概览实际请求。新增近期调用需要分别请求成功与错误日志，不把功能新增请求误称为零成本优化。

首轮证据保存在 `.codex-artifacts/wanchuan-console-20260905/`。最终测试、构建、浏览器脚本、日志及截图保存在 `output/playwright/`。

## 最终验收结果

环境：Windows、Node v25.8.1、Go go1.26.5 windows/amd64、Rsbuild v2.1.6。

以下命令均退出 0：

| 工作目录 | 命令 | 结果 |
| --- | --- | --- |
| `web/` | `node node_modules/vitest/vitest.mjs run src/features/dashboard src/features/keys src/features/wallet src/features/profile src/features/usage-logs src/hooks/__tests__ src/context/__tests__ src/components/layout src/components/__tests__/search.test.tsx src/lib/__tests__/theme-preset-labels.test.ts` | 39 文件、171 用例通过 |
| `web/` | `node node_modules/@typescript/native-preview/lib/tsgo.js -b` | 类型检查通过 |
| `web/` | `node node_modules/oxlint/bin/oxlint -c .oxlintrc.json <本次变更及新增的 TS/TSX 文件>` | 无 lint error |
| `web/` | `node ../output/playwright/check-changed-format.mjs` | 39 个代码/样式文件无格式差异，版权头保留 |
| `web/` | `node node_modules/@rsbuild/core/bin/rsbuild.js build` | 生产构建通过 |
| 仓库根目录 | `go test -count=1 ./relay/helper ./relay/common ./relay/channel/openai ./relay/channel/deepseek ./relay/channel/volcengine ./relay/channel/xai ./setting/billing_setting` | 通过；volcengine 当前无测试文件 |
| 仓库根目录 | `go test -count=1 ./controller -run 'TestOptionsExposeEffectiveBilling\|TestOptionsExposeEmptyBillingMapsAsEmptyObject\|TestUpdateOptionRejectsInvalidTaskBillingExpressions\|TestUpdateOptionRejectsUsageExpressionWithoutTaskPlugin\|TestUpdateOptionAliasBillingExprUsesPluginSchema'` | 有效计费配置与表达式校验回归通过 |
| `relaykit/` | 设置 `GOWORK=off` 后执行 `go test -count=1 ./relayconvert/reasoning/...`、`go build ./...` | 独立测试及构建通过 |
| 仓库根目录 | `git diff --check` | 无空白错误 |

浏览器验收使用本地只读 API 样本，涵盖七语言、概览/密钥/日志/钱包/资料五页面、明暗主题、1440/390/320px，共 210 组布局检查。接入引导键盘展开、30 天日历桶、消费/请求切换、主题持久化、完整密钥显式读取及 Escape 焦点返回、手机侧栏和主题设置均通过。

复制验证确认：复制 Base URL 不读取完整密钥；复制 curl 只读取一次完整密钥，写入浏览器剪贴板；屏幕中的请求示例仍保持 `sk-...`。未向任何上游发送示例请求，未创建实际支付。

### 资源与请求对照

- 基线为 `a00381c00`；对照目录中 1264 个源代码、样式、配置和锁文件在统一换行后与 Git 基线一致，基线与当前构建共用同一安装依赖目录。
- HTML 引用的入口 JS/CSS：基线 2,696,254 字节、gzip 710,952 字节；当前 2,694,471 字节、gzip 710,801 字节，gzip 减少 151 字节（约 0.02%）。这是入口资源对照，不代表所有异步页面资源或生产网络时延。
- 使用已有 status 缓存的页面重载：基线 6 个 API 请求，当前 9 个。新增 1 次新鲜账户余额读取和成功/错误日志各 1 次读取；七日汇总与趋势共用 1 次用量请求。
- 概览 → 钱包 → 概览的缓存有效期内导航不重复请求 `/api/user/self`。钱包新增读取为流水、余额拆分、充值配置及金额计算，未发起支付。
- 不宣称总请求数下降或生产端到端速度提升。网络与复制证据：`output/playwright/wanchuan-network-and-copy-qa.log`；入口资源明细：`.codex-artifacts/wanchuan-console-20260905/initial-assets.json`。

## 发布边界

- 当前仅本地提交，不部署、不推送、不触达实际支付。
- 后端兼容修复已单独提交为 `0b9853df0`；前端皮肤、回归测试与本文档单独提交，原有三个 `web/scripts/` 改动不纳入。
- 本地预览使用 `127.0.0.1:5182` 的独立只读样本服务，不是生产账户；界面代码来自正式构建产物。
- 默认 `bun` 路径在本机没有输出，因此验证使用已安装依赖的 Node CLI，不改包管理器、依赖或锁文件。
- 本次无数据库行为变化；数据库三引擎矩阵不属于此次验证结论。后续如修改数据库相关代码，仍需按项目规范验证真实 SQLite、MySQL、PostgreSQL。
- 未运行全仓库 Go/前端测试套件、生产账户或真实支付验收；本报告只覆盖列出的代码路径与本地样本。
- 回退主题可选择原 `default` 预设；发布时应单独验收两组提交，并保留上一版镜像及前端产物。
