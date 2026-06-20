# 按规格机械生成 API 测试

读取 `design/base/API/<API-XXX>/` 下的 `test_case.md` 三张表，按 `guidelines/test-spec.md` 的契约**机械生成** fixture 目录与测试代码骨架。

本命令是**机械路径**——只抽取、不发挥；契约不全则停。

## 设计哲学

`dev-test-gen` 是 **Claude 执行的 slash command**，不是 Python 脚本。设计契约规则时：

- ✅ **AI 模式生成**：用 Claude 的语义理解 / 业务推断 / 自然语言解析能力，识别 `test_case.md` 等人类文档的意图——这是 AI 本职
- ✅ **防漂移**：确定性来源是**输出端**的硬约束（字段顺序、`weakRules` 字典序、占位变量封闭集、字面值取 DDL DEFAULT 等）
- ❌ **不要求作者用严格格式 / 封闭句式**：`test_case.md` 是人类文档，作者按自然方式写就行；契约**不**枚举"必须以 `?` 起首" / "必须用某固定句式" 这种字面要求
- ❌ **不退化为字面匹配 / 不调 LLM**：如果按这条解读，dev-test-gen 等价于 Python 脚本，要 Claude 来跑没意义

### 输入端 vs 输出端对偶

| 端 | 对象 | 规则 |
|---|---|---|
| **输入端** | `test_case.md` / `03 yaml` / `04 docs` / `06 表定义` / `design/base/*` | Claude 用 AI 解析，**容忍格式变体、自然语言、全/半角混杂、空白瑕疵、句式变体**；不维护"封闭句式表"（除非语义边界要求） |
| **输出端** | `ita/<API>/<CASE>/*.json` / `*.sql` / `<op>_test.go` | **严格防漂移**：字段顺序按 DDL / `weakRules` 字典序 / 占位变量限封闭集 / Mock 字面值取 DDL DEFAULT / 错误用 `errors.New(<表 C 原文>)` 等硬约束 |

**对偶不冲突**：输入容忍多样，输出收敛到确定。本契约后续所有"识别 / 抽取 / 解析"规则按此对偶模型组织——别处见到"输入端字面匹配的封闭句式表"是历史遗留，按本哲学修订。

### "机械路径，不发挥" 的正确含义

- ✅ **不臆造文档没写出的字段值 / 实体 / 业务规则**——只按真相源（02/03/04/06/design）已写出的内容生成
- ✅ **不替设计师 / 产品 / 上游做决定**——遇到 docs 跟实装冲突 → stop + 报警，不暗中选一边
- ❌ **不是**"字面字符串匹配 / 不解析自然语言 / 不调 LLM"

## 硬约束清单（关键提示，sub-agent 必须遵守）

以下硬约束(共 14 条)在 Step 详述,**先在此列出避免遗漏**。任一项不满足时停机或 fallback,**不暗中绕过**:

### 输出端确定性(防漂移)

1. **业务实体 pubkey / ID** 必须取 03 yaml example 字面 HEX,**禁止**自创占位变量
2. **共享运行时变量**限封闭 3 个 `${callerPubkey}` / `${channelId}` / `${apiPath}`,其它一律字面值
3. **Mock 字面值**取 DDL DEFAULT;错误用 `errors.New(<表 C 原文>)`;一律 `.Return(...)` 不用 `.DoAndReturn`
4. **`weakRules` map** 输出按 key 字典序
5. **path 占位** 用 `strings.Replace` 就地替换(`apiPath + "?" + queryString`),不重新组路径
6. **`fx.Populate`** 仅 `&testEngine, &testDB`,不抽其它包级 var
7. **禁止抽自定义辅助函数** —— 不写 `runIntegrationCase` 等;每个 CASE 函数内联 `util.*` 调用
8. **数据库表 INSERT 字段顺序** 严格按 06-表定义.md DDL 列顺序

### docs / 实装漂移处理

9. **docs(02/03/04)跟实装字面冲突时,优先级**:
   - (a) 本仓 `design/base/API/<API-XXX>/test_case.md` **断言口径段** 已显式约定 → 按 test_case.md 走,**不 stop**
   - (b) 本仓 `questions.md` 已有 **QA-X 已确认** 裁决 → 按 questions.md 走,**不 stop**
   - (c) 上述均无 + docs / 实装字面冲突 → **stop + 报警**,等作者补登记 test_case.md / questions.md;**不暗中选一边**
10. **`API-<编号>.md` 顶部不需要"已知 docs bug 修正规则"段**;漂移登记走 (a)/(b) 两条路径(本仓约定,简化结构)

### 输入端容忍

11. **test_case.md** 是人类文档,容忍格式变体 / 全角半角 / 句式变体;Claude 用语义解析意图

### Sub-agent 沙箱模式边界(严禁项)

12. **沙箱模式下** sub-agent **严禁**:写入 `ita/<API>/`(promote 是主会话职责) / 跑 `go test` / `git commit` / `git push` / 写 `.claude/` / 写 `internal/` / 写 `design/`;**只允许**写沙箱目录 `<TEST_DIR>`(test-sandbox/`<标签>`/`<API>`/)
13. **响应 body 字面值优先级**(`msg` / `data` / 等所有响应字段):**test_case.md 断言口径段 > 实装(`xerror.Error.Msg` / `response.Error` 字面 / DTO 默认值) > 03 yaml example > 04 短文案**;沙箱生成 response.json 异常路径默认 `data: {}`(实装 `response.Error` 字面,非 `null`);`msg` 异常路径默认 `[XX-NNN] xxx`(实装 `xerror.Error.Msg` 字面);正常路径 `msg: "成功"`(实装);除非 test_case.md 明确指定其它值

### CASE 级生成日志(append-only)

14. **每个 CASE 生成过程中** sub-agent 在 `<CASE-id>/_report.md` **append 决策日志**
   (`.gitignore` 已含 `*_report.md`,本地保留不入 git)。

   **格式**:`[Step X.Y] <事件>:<细节>` 每决策一行;不要求结构化小节、不要事后整理。

   **每 CASE 必记 6 条**(append-only,按生成时序):
   - `[Step 5.2] request.json:<整体替换|字段覆盖|原样>;覆盖字段 <清单>`
   - `[Step 5.3] data.sql:<N> 行 INSERT(<表清单>)` 或 `no seed`
   - `[Step 5.4] response.json:msg <test_case.md|实装|03|04>;data <实装{}|null|其它>`
   - `[Step 5.5] result.sql:<N> 条 SELECT;WHERE 字段 <清单>` 或 `expect_noop`
   - `[Step 6] 测试函数:层级 <integration|service-mock>;追加 TestCASE_NNN`
   - `[summary] CASE-NNN:文件 4 个,TODO_FIXTURE=N TODO_RULE=N TODO_MOCK=N`(最后写)

   **遇决策歧义时加记**(可选,平均 1-3 行):
   `[决策] <问题> | 候选 <A/B> | 选 <X> | 原因 <契约 #N / design ref>`

   预期每 CASE ~10 行;append 是常数级开销,不让 sub-agent 事后整理。查看仅供主会话 / 人工 review;PR 提交不带。

## 使用场景

按调用目的选合适的颗粒度，避免无意义的全量重跑：

| 场景 | 命令形态 | 颗粒度 | 适用时机 |
|---|---|---|---|
| **轻量契约验证** | `/dev-test-gen <API> @case/<CASE-NNN> @sandbox/contract-verify` | 单 API 单 CASE 沙箱 | 修订了某条 dev-test-gen.md / test-spec.md 规则后，挑最能体现该规则的 CASE 跑一遍，人工 diff 关键产物验证生效 |
| **代表性整体验证** | `/dev-test-gen <API> @sandbox/<标签>` | 单 API 全 CASE 沙箱 | 修订多条契约 / 想看某 API 整体效果 |
| **全量沙箱实测**（双轮 + 实跑测试） | 5 个 API 并行各跑 2 轮 + 各轮跑 `go test` | 全 API 全 CASE 沙箱 + 实跑 | PR 收尾、契约批量定稿。验证标准：**两轮测试通过率一致** = 防漂移生效（容忍等价漂移，只防真漂移） |
| **正式生成 / 重生成** | `/dev-test-gen <API>` | 单 API 全 CASE，产物入 `ita/` | 实装阶段产出 / 重生成正式测试代码 |

## 输入

`$ARGUMENTS` = `<目标> [@case/<CASE-NNN>] [@sandbox/<标签>]`（两个 `@` 后缀可任意顺序，任选其一或都用）。

**目标**（二选一，格式与示例）：

| 形式 | 格式 | 示例 |
|------|------|------|
| API 目录名（推荐，最明确） | `API-<编号>-<operationId>` | `API-001-createChannel` |
| 工单号（从分支或 JIRA 关联反查） | `MOS-<数字>` | `MOS-3070` |

**`@sandbox/<标签>` 可选后缀**（沙箱模式，标签任意短串，建议 `round-<N>`）：

- 不传 → 输出到正式目录 `ita/<API-XXX>/`
- 传入 → 输出到沙箱目录 `test-sandbox/<标签>/<API-XXX>/`
- 沙箱目录由 `.gitignore` 排除，不污染 git；适合多轮生成、漂移对比

**`@case/<CASE-NNN>` 可选后缀**（单 CASE 模式）：

- 不传 → 处理**全部** CASE
- 传入 → 仅处理该 CASE（fixture + 主测试文件中追加该 `TestCASE_NNN`）
- 主测试文件已存在则**不重写**顶部（包级常量 / TestMain / `truncateTables` / `weakRules`），仅追加函数；不存在则一并生成
- 单 CASE 模式下 Step 3 三方差异仅比对该 CASE

省略「目标」时按以下优先级推定：
1. 当前分支名中的工单号 → JIRA 中关联的 API 目录
2. `git diff main...HEAD --name-only` 中包含的 `design/base/API/<API-XXX>/` 路径
3. 失败则报错退出，要求显式传参。

**示例调用**：
- `/dev-test-gen API-001-createChannel` → `ita/API-001-createChannel/`（全 CASE）
- `/dev-test-gen API-001-createChannel @sandbox/round-1` → `test-sandbox/round-1/API-001-createChannel/`
- `/dev-test-gen API-001-createChannel @case/CASE-008` → 仅 `CASE-008/` + 主测试文件追加 `TestCASE_008`
- `/dev-test-gen API-001-createChannel @case/CASE-008 @sandbox/round-1` → 单 CASE + 沙箱组合

## 工具链依赖

- **`mockgen`** 可执行（`go install go.uber.org/mock/mockgen@latest`，确保 `$(go env GOBIN)` 在 `$PATH`）。`ita/mocks/` 产物由本命令自动维护，详见 Step 1.5。

## 参照文档

| 文件 | 读取时机 | 用途 |
|------|---------|------|
| `.claude/guidelines/test-spec.md` | 全程 | 契约依据，所有 schema / 骨架按此 |
| `design/base/API/<API-XXX>/test_case.md` | Step 2 | 三张表的抽取源 |
| `design/base/API/<API-XXX>/params_check.md` | Step 2 | 字段类型 / binding（用于占位字符串展开） |
| `design/base/API/<API-XXX>/db_data.md` | Step 5 / Step 6 | `truncateTables` 表清单、`result.sql` 模板的 INSERT 步骤来源 |
| `design/base/API/<API-XXX>/API-<编号>.md` | Step 5 | HTTP 端点（method + path）、已知 docs bug 修正口径 |
| `~/work/MosaviJP/Mosavi-docs/频道/技术文档/03-API定义.yaml` | Step 5 | `requestBody.examples.<...>.value` / `responses.'200'.examples.<...>.value` 抽取（按 `operationId` 索引） |
| `~/work/MosaviJP/Mosavi-docs/频道/技术文档/04-响应码定义.md` | Step 5 | 错误码表中的方括号短文案，作为异常响应 `msg` 字面值 |
| `.claude/guidelines/coding.md` 与 `coding/004-controller.md` / `coding/005-service.md` | Step 6 | 集成 / mock 骨架的语义参照 |

> 本命令只读结构化数据（03 yaml 节点、04 表格列）；自由叙述（02 业务流程等）一律不读。03 与其它叙述源冲突时按 `API-<编号>.md` "已知 docs bug 修正规则" 为准。

## 读取边界（硬约束）

为避免读取无关文件污染上下文 / 引入漂移，本命令 Read / Glob / Grep 严格限定在以下路径。

**白名单**（仅允许读取）：

- `.claude/commands/dev-test-gen.md`（本命令自身）
- `.claude/guidelines/test-spec.md`
- `design/base/API/<API-XXX>/`（`test_case.md` / `params_check.md` / `db_data.md` / `API-<编号>.md` / `questions.md`）
- `design/base/06-表定义.md`（DDL 真相源：生成 `truncateTables` / `result.sql` 字段值时按需读）
- `~/work/MosaviJP/Mosavi-docs/频道/技术文档/03-API定义.yaml`（按 `operationId` 索引）
- `~/work/MosaviJP/Mosavi-docs/频道/技术文档/04-响应码定义.md`（按 code 索引方括号短文案）
- `ita/util/*.go`（**仅查看公开 API 签名**作生成参考；不修改）
- `.claude/guidelines/coding.md` 与 `coding/004-controller.md` / `coding/005-service.md`（骨架语义参照；可选）

**条件白名单**（仅 `service-mock` / `repo-mock` 层级 CASE 需要时）：

- `internal/repo/<api>.go`（抽 repo 接口与方法签名）
- `internal/service/*.go`（构造 service 实例时参考；按**方法名** grep `func .* <operationId>` 找到承载文件，service 实装通常按业务域命名如 `member.go` / `channel.go`，**不按 API operationId 命名**）
- `internal/model/gorm/`（mock CASE Return 值类型如 `*modelgorm.<X>`）
- `internal/pkg/apicontext/`（mock CASE 构造 `apicontext.ApiContext` 时需查无参构造函数签名；仅查看公开 API，不读其它实现细节）
- `ita/mocks/<repo>_mock.go`（mockgen 产物，引用 `mocks.NewMock<RepoName>`）

**黑名单**（禁止读取）：

- `ita/<API-XXX>/`
- `test-sandbox/round-*/`
- `internal/infra/` / `internal/controller/` / `internal/app/` / `internal/server/`
- `internal/model/`（除条件白名单中的 `internal/model/gorm/`）
- `internal/pkg/`（除条件白名单中的 `internal/pkg/apicontext/`） / `internal/util/` / `internal/constants/`
- `Mosavi-docs/` 中除 03 / 04 之外的文件
- `env.toml` / `deploy/` / `.idea/` / `.git/` / `cmd/` / `docs/`
- `go.mod` / `go.sum`

**违规处理**：需要白名单 / 条件白名单外的文件 → 停机报告，由人工决策；不可先读后问。

## 输入归一化（容忍人类自然瑕疵）

`test_case.md` 是人类文档，输入法 / 排版习惯会带来全角半角混杂、多余空白等瑕疵。生成器在**抽取三张表的任何单元格之前**先做归一化，避免因字符级失误把大量字段判为 `TODO_*` 而失去机械生成的意义。

**归一化范围**（所有列单元格内容文本）：

| 归一化项 | 输入 → 输出 |
|---|---|
| 全角字母 → 半角 | `Ａ-Ｚ` / `ａ-ｚ` → `A-Z` / `a-z` |
| 全角数字 → 半角 | `０-９` → `0-9` |
| 全角标点 → 半角 | `：` → `:` / `，` → `,` / `．` → `.` / `；` → `;` / `？` → `?` / `！` → `!` / `＝` → `=` / `＋` → `+` / `（）` → `()` / `［］` → `[]` / `｛｝` → `{}` / `〈〉` → `<>` / `「」` → `""` / `『』` → `""` / `〜` → `~` / `／` → `/` / `＼` → `\` / `＆` → `&` |
| 全角引号 → 半角 | `"` / `"` → `"` |
| Unicode 空白 → 半角空格 | NBSP / 全角空格 → 普通空格 |
| 前后空白 trim、内部多余空白压缩为单空格 | `  name :  "x" ` → `name : "x"` |

**归一化不处理**（仍走 `TODO_*` 路径）：

- 错别字（如 `desription:` → `desription` 不被改为 `description`）
- 字段名跟 `params_check.md` 不一致（生成器**不强制**校验字段名集；字段不存在时仍按字面写入 fixture，由实装阶段类型校验暴露）
- 整段未命中任何模式表

**实施约束**：归一化是 dev-test-gen 私有 pipeline 步骤，**不修改 `design/base/` 下任何文件**——只在内存里把读入的字符串做归一化后再走模式匹配。设计文档原文保持人类自然写法。

## 执行步骤

### Step 0: 上下文定位

1. 解析 `$ARGUMENTS`：
   - 拆出「目标」+ 可选 `@sandbox/<标签>` + 可选 `@case/<CASE-NNN>`（两个 `@` 顺序无关）
   - 确定 `<API-XXX>` 目录名（按 `## 输入` 章的规则）
2. 设置工作变量：
   - `SPEC_DIR = design/base/API/<API-XXX>`（沙箱模式下不变，**输入侧始终读真实 design**）
   - `TEST_DIR`：
     - 无 sandbox 后缀 → `ita/<API-XXX>`
     - 有 sandbox 后缀 → `test-sandbox/<标签>/<API-XXX>`
   - `SANDBOX_LABEL = <标签>`（若有，用于 Step 7 报告标识）
   - `SINGLE_CASE = <CASE-NNN>`（若有 `@case/` 后缀；否则为空）
3. 分支确认：
   - `git branch --show-current`
   - `git status` 是否有未提交变更（有则提示，但不阻止）
4. **沙箱模式特殊语义**：
   - 不会触碰正式 `ita/<API-XXX>/`，全部输出落到 `test-sandbox/<标签>/<API-XXX>/`
   - S2 / S3 按 `TEST_DIR`（沙箱路径）实际计算；diff / 不覆盖 / 不删除等规则不变
   - 想要"全新一份"重跑同一 round 时，由用户先 `rm -rf test-sandbox/<标签>/<API-XXX>/`，再调用本命令。或换一个 round 标签
5. **单 CASE 模式特殊语义**（`SINGLE_CASE` 非空）：
   - Step 1 契约预检照常执行（验整份 test_case.md 完整性）
   - Step 2 抽取三张表照常，但后续步骤只对 `SINGLE_CASE` 处理
   - Step 5 仅生成 `<TEST_DIR>/<SINGLE_CASE>/` 一组 fixture
   - Step 6 主测试文件**已存在则不重写顶部**（包级常量 / TestMain / `truncateTables` / `weakRules`），仅追加 `TestCASE_<NNN>` 函数；不存在则一并生成（注意：顶部生成时 `truncateTables` / `weakRules` 仍按表 A/B/C 全表展开，因为这些是包级共享）
   - Step 3 三方差异仅比对该 CASE 是否在 S1 中
   - Step 7 报告仅描述该 CASE

### Step 1: 契约预检（硬门）

按 `test-spec.md` §2 逐项校验 `SPEC_DIR`：

- [ ] 必备文件齐全：`API-<编号>.md` / `params_check.md` / `db_data.md` / `test_case.md`
- [ ] `test_case.md` 含且只含三个 H2：`## 测试用例` / `## 断言口径` / `## 测试层级`
- [ ] 表 A 列名为 `编号 / 场景 / 入力参数 / 期望响应 / 初始数据 / 备注 / 说明`，且每行 `编号` 列匹配 `^CASE-\d{3}$`，自 `001` 起连续无跳号
- [ ] 表 B 列名为 `字段 / 弱断言规则`
- [ ] 表 C 列名为 `用例 / 层级 / 说明`，「层级」列取值经**内部识别表**（见 Step 2）映射后 ∈ {`integration`, `service-mock`, `repo-mock`}
- [ ] 表 C 用例覆盖表 A 全部编号（差集为空）；「用例」列区间分隔符 `~` 允许带或不带空格
- [ ] 单 CASE 模式（`SINGLE_CASE` 非空）：`SINGLE_CASE` 必须在表 A CASE id 集合内
- [ ] **工具链**：`mockgen --version` 可执行（不可执行 → 停机报错，提示 `go install go.uber.org/mock/mockgen@latest`）

任一项不满足 → **停止，输出修复清单，退出**。不得绕过、不得脑补缺项。

> 修复清单格式：`[契约破损] <文件>:<位置>: <现状> ← 期望 <schema>`。修复后请用户重新调用 `/dev-test-gen`。

### Step 1.5: `ita/mocks/` 同步（每次自动跑）

把 `ita/mocks/` 维护成跟 `internal/repo/` 当前接口集严格一致。

1. **扫接口**：列 `internal/repo/*.go`（不含 `0module.go` 等 fx 装配文件），grep `^type \w+Repository interface {` → `IFACE_SET = {ChannelRepository, MemberRepository, ...}`
2. **比对 generate.go**：读 `ita/mocks/generate.go` 现存 `//go:generate mockgen ...` 行末接口名 → `GEN_SET`
3. **若 `IFACE_SET != GEN_SET`**：
   - 重写 `ita/mocks/generate.go`：保留包注释 + 包声明，**按字母序**生成全部 `//go:generate` 指令（产物文件名约定 `<小写接口名去 Repository 后缀>repo_mock.go`，如 `ChannelRepository` → `channelrepo_mock.go`）
   - 删除 `ita/mocks/*_mock.go` 中不对应当前 `IFACE_SET` 的孤儿产物
4. **跑** `go generate ./ita/mocks/...`（无条件跑——保证产物跟接口签名最新一致）
5. Step 7 报告里描述 mocks 同步结果（新增 / 删除 / 接口集无变化）

**异常**：

- mockgen 报错 → 停机，把 stderr 完整透传报告
- `internal/repo/` 找不到任何接口 → 警告但不报错，`ita/mocks/` 保持空

### Step 2: 抽取三张表为机器结构

仅抽取**结构**（编号、code、层级、字段名），**不解析语义**（入参样本、mock 行为、msg 文案等）。

```
cases = [
  { id: "CASE-001", scene: "<原文>", expected_code: 0, remark: "<原文>" },
  ...
]
assert_fields = ["data.metadata.channelId", "data.metadata.createdAt", ...]  // 仅字段名列表，不抽 DSL 规则
case_layer    = { "CASE-001": "integration", "CASE-008": "service-mock", ... }
```

抽取规则（**机械、确定**）：

- `expected_code`：从「期望响应」列**首部**抽 `code=<整数>`，提取失败 → 回 Step 1 列入修复清单。其它内容（关键字段简述）整段视为原文 `remark`，不解析。
- 区间写法 `CASE-001~CASE-007` / `CASE-001 ~ CASE-007`（前后空白）展开为单编号清单。
- `case_layer[id]`:Claude 按语义判定表 C「层级」列原文 → 输出端封闭三值 `{integration, service-mock, repo-mock}`(骨架差异要求)。作者写法不受限——`integration` / `集成` / `走真 PG` 等同等识别 integration;`service-mock` / `mock repo` / `service mock` 等同等识别 service-mock;以此类推。**确实无法判定**(业务意图不清)才回 Step 1 修复清单。

- 表 A「入力参数」列原文整段保留为 `cases[i].input_raw`，**Step 5.2 由 Claude 语义解析**——理解作者的覆盖 / 整体替换 / query 意图，结合 03 yaml 基线生成 `request.json`。解析失败（业务意图模糊到无法判定）时 fallback 到原文 + `TODO_FIXTURE` 注释
- 表 A「初始数据」列原文整段保留为 `cases[i].seed_raw`，**Step 5.3 由 Claude 语义解析**——结合 06 表定义生成 INSERT。详见 Step 5.3 防漂移硬约束
- 表 B「弱断言规则」列原文整段保留为 `assert_fields[i].rule_raw`，**Step 6.4 由 Claude 语义解析**——翻译为输出端封闭的 mini-DSL（`regex:` / `timestamp:near` / `length-ge:` / `prefix-join:`）。解析失败 fallback 到 `TODO_RULE` 注释
- 表 C「说明」列原文整段保留为 `case_layer[id].mock_raw`，**Step 6.3 由 Claude 语义解析**——结合 `internal/repo/` 接口签名生成 gomock EXPECT 链。详见 Step 6.3 字面值确定性 + 防漂移规则

### Step 3: 计算三方差异

```
S1 = cases 中的全部 CASE id
S2 = ls TEST_DIR/CASE-* 的目录名集合（不存在则空）
S3 = grep "^func TestCASE_\d{3}" TEST_DIR/*_test.go 的函数名集合（不存在则空）
```

**单 CASE 模式**（`SINGLE_CASE` 非空）：仅判定该 CASE 在三集中的存在性，输出"是否要新建" + "是否已存在 (跳过)"两态；不展示全表差异。

**全 CASE 模式**：计算并向用户**展示三段差异**：

| 集合 | 仅在 S1 | 仅在 S2 | 仅在 S3 |
|------|---------|---------|---------|
| 含义 | 要新建 | 多余目录 | 多余函数 |
| 处置 | 生成 | 列出，等用户处置 | 列出，等用户处置 |

### Step 4: 用户确认门（硬门）

向用户陈述：

- 要新建的 CASE 列表（id + 层级 + 场景一句话）
- 不动的 CASE 列表（已存在目录 / 函数，本命令不覆盖）
- 多余项（用户后续手动处理，本命令不删）

**等待用户确认**：「按此清单生成可以吗？」

- 同意 → Step 5
- 拒绝或要调整 → 停止，回到用户

> **沙箱模式自动同意**：调用时带 `@sandbox/<标签>` 后缀视为用户已显式授权多轮生成，本步骤跳过等待，直接进入 Step 5。

### Step 5: 生成 fixture

对每个**要新建**的 CASE id：

1. 创建目录：
   ```
   TEST_DIR/<CASE-id>/input/
   TEST_DIR/<CASE-id>/output/
   ```

2. 生成 `input/request.json`：

   **数据基线**：03 yaml `paths.<path>.<method>.requestBody.content.<mime>.examples.<first>.value` 抽出的 YAML 节点（按 `operationId` 索引；多个 example 时取第一个）。
   - 正常路径（`expected_code=0`）：基线**原样**写入，除非表 A 入力参数列显式说"整体替换"或字段覆盖。
   - 异常路径（`expected_code≠0`）：**Claude 解析**表 A「入力参数」列业务意图，结合 03 yaml 基线生成 `request.json`。
   - Claude 无法解析意图（业务描述模糊 / 跟 03 schema 字段冲突）→ 写入基线 + 标 `TODO_FIXTURE`，附带表 A 原文。

   **覆盖语义（两种）**：

   - **字段覆盖**（默认）：在基线上"修改指定字段值"，其它字段保留基线原值。例：基线 `{name:"摄影爱好者",description:"...",avatar:"..."}` + 表 A 写「`name: ""`」→ 结果 `{name:"",description:"...",avatar:"..."}`（**保留** description / avatar）。
   - **整体替换**：表 A 写**字面 JSON**（`{}` / `{key: value}` 完整对象）时忽略基线，直接用该 JSON 作 body。例：`{}` → `request.json={}`（CASE-005 请求体为空）；`{"description":"X"}` → `request.json={"description":"X"}`（仅传 description）。

   **表 A 入力参数列常见写法示例**(不作枚举,Claude 按语义识别):

   - `{}` / `{<完整 JSON>}` → 整体替换(忽略基线)
   - `<field>: ""` / `<field>: "<value>"` / `<field>: <N 字符>` / 多字段逗号分隔 → 字段覆盖(基线 + 覆盖)
   - `任意合法 body` / `metadata 合法(...)` → 基线原样

   > **"仅改 X 字段"写法选择**:表 A 写 `{"description":"新值"}` 整体替换 vs `description: "新值"` 字段覆盖——两种都合法,作者按 CASE 设计意图选择。

   **输出端防漂移**:字段顺序不重排,字符串字面跟表 A 完全一致(不同义改写)。

   **GET 接口的 query 参数处理**（GET 无 body）：

   - `request.json` 不生成（或生成空文件 `{}` 作占位，避免 fixture 文件缺失）
   - query 参数从表 A「入力参数」列抽，识别规则：**含 `=` 字符** 的入参视为 query 串（容许 test_case.md 的人类自然写法，如 `since=0` / `page=0&pageSize=1001` / `?since=0` / `role=admin&keyword=foo`）
     - `?` 前缀**可选**，识别时先去掉首字符 `?`
     - 多参数用 `&` 分隔（标准 URL query 形态）
   - 测试函数发请求时把 query 串拼到 path 末尾，统一形态 `apiPath + "?" + queryString`（生成器拼接时补 `?` 前缀）
   - 表 A 入力参数列**不含 `=`** 的字段覆盖语法（如 `name: "..."`、`{...}` 整体替换）按上方 body 覆盖模式处理，与 query 参数语法天然区分
   - 表 A query 串含未定值（如 `since=<上次同步 ms>`） → 写入测试函数时保留占位 + `TODO_FIXTURE` 注释，由人工补具体值

3. 生成 `input/data.sql`（Claude 解析「初始数据」列业务自然语言 + 结合 06 表定义生成 INSERT）：

   `dev-test-gen` 是 Claude 执行的 slash command，**理解业务自然语言、翻译为 SQL 是 AI 本职**——这不算"发挥"。"机械路径"指**不臆造文档没写出的字段值**，不是"字面字符串匹配 / 不调 LLM"。

   **解析输入源**：
   - 表 A「初始数据」列原文（业务自然语言，如「已存在一个频道；调用者是频道主；群内 1 名管理员；邀请链接已生成」）
   - `design/base/06-表定义.md`：相关表的 DDL（字段名 / 类型 / 约束 / DEFAULT）
   - `~/work/MosaviJP/Mosavi-docs/频道/技术文档/03-API定义.yaml`：字段业务语义辅助
   - 表 A 其它列上下文（入力参数 / 期望响应）：辅助推断 caller 角色 / channel id 等

   **输出 INSERT 形态**：
   - 每个被提到的实体一条 `INSERT INTO <表> (字段...) VALUES (值...);`
   - 多张表用 `;` 分隔 + 换行
   - 头部加 audit 注释保留原文：`-- 初始数据（test_case.md 表 A）：<原文整段>`

   **防漂移规则**(遵守顶部硬约束清单 §1-3, §8):

   1. **表顺序**: 按 `db_data.md` INSERT 步骤逆推(被依赖表先 seed);GET 接口的只读表按 06 表外键依赖关系判断
   2. **字段顺序**: 严格按 `06-表定义.md` DDL 列顺序
   3. **DDL DEFAULT 字段省略**: 值不确定 / 时序字段(`NOW()` / `BIGSERIAL` / `gen_random_uuid()`)**必须省略**;其它 DEFAULT 字段 Claude 按业务理解决定写或省略(两种业务等价,等价漂移容忍)
   4. **值规则**: 共享变量限 §3.5.1 封闭 3 个;**业务实体 pubkey / ID 必须取 03 yaml example 字面 HEX,禁自创占位**;DDL 约束 padding Claude 按业务生成满足约束即可(等价漂移容忍)
   5. **不臆造字段**: 业务描述未提及的可选字段省略走 DEFAULT
   6. **多 CASE 一致性**: 同一概念用同一字面值(同 channelId / 同 owner pubkey),CASE 间只增量差异
   7. **N 个未具名实体**: 从 03 yaml example 数组取前 N 个字面 HEX;不够 → fallback TODO_FIXTURE

   **异常处理**:

   - 业务描述跟 06 / 03 冲突 → **stop + 报警**(硬约束 §9)
   - 业务描述模糊到无法解析 → 回退 TODO_FIXTURE,Step 7 报告高亮
   - 业务描述跟入力参数冲突 → stop 报警

4. 生成 `output/response.json`：

   - 正常路径（`expected_code=0`）：
     - 抽 03 yaml `responses.'200'.content.<mime>.examples.<first>.value` 作为基线。
     - 解 YAML anchor / alias，输出展开后的 JSON。
     - **可写字段三态处理**（来自 03 yaml `requestBody` 引用的 schema 字段集 / `params_check.md` 列出的请求字段）：
       1. **request 显式传值** → 取 request 最终值（含 Step 5.2 字段覆盖结果）
       2. **request 未传但 seed `data.sql` 有该字段值**（partial update 场景，CASE-002 "仅改 X" 类） → 取 **seed 字面值**（保持 partial update 语义:未传不改 DB）
       3. **request 未传且 seed 也没该字段** → 取 03 example 字面值
     - **派生字段 / 服务端计算字段**（不在 `params_check.md` 请求字段集 + 不属于 DDL 固定字面值，如 `memberCount` COUNT 派生 / `createdAt` 时间戳 / 服务端生成 id 等）：**必须**在 `test_case.md` 表 B 列弱断言；否则生成器在 Step 7 报告里高亮警告（这类字段全等比对必失败）。03 example 字面值原样保留作 fixture baseline，弱断言保证测试不卡死。
     - 按 `API-<编号>.md` 顶部的"已知 docs bug 修正规则"做字段覆盖（见下方说明）。
   - 异常路径（`expected_code≠0`）：写模板：
     ```json
     {"code":<expected_code>, "msg":"<04 短文案>", "data":null}
     ```
     `<04 短文案>` = 04 文档错误码表中对应 code 的方括号字面值去括号（如 `[参数校验失败]` → `"参数校验失败"`）。

   **已知 docs bug 修正规则**（按 `API-<编号>.md` 顶部声明）：

   - 当 03 yaml example 中某字段与业务实际不符时，由 `API-<编号>.md` 自行登记修正条目（字段路径 + 新值 / 删除 / 重写规则）。
   - 本命令按 `API-<编号>.md` 中显式声明的条目逐条覆盖；无条目则不动。
   - 本契约不内置示例条目；具体修正参考各 `API-<编号>.md`。

   **Claude 主动跨文档检测**（防 PR 漏维护修正表的兜底）：

   - Claude 抽取 03 yaml example 时，**同时对照** `internal/model/res/*.go` 实装结构（条件白名单）与 `design/changes/MOS-*.md` 设计变更文档，检测以下冲突：
     - 03 yaml example 字段在实装结构中**不存在**（如 PR #48 把 `metadata.inviteLink` 扁平化为 `linkCode`，03 未回写）
     - 03 yaml example 字段类型 / 嵌套结构跟实装不一致
     - 03 yaml example 字段值跟实装常量 / DDL DEFAULT 不一致（如 `state: "blacklisted"` vs 实装 `stateIsBlocked="isBlocked"`）
   - 发现冲突 → **stop + 报警**，列出冲突详情（03 字段路径 / 03 字面值 / 实装实际值 / 推断来源），提示作者：
     1. 在 `API-<编号>.md` 顶部"已知 docs bug 修正规则"表登记修正条目，或
     2. 推动 Mosavi-docs 修订 03 yaml example
   - **不**暗中选一边自动修正——这是设计师 / 上游决策权，不该 Claude 替代

   若实装运行后发现派生字段字面与 03 example 不一致，先在 `API-<编号>.md` 补一条修正规则（或扩 test_case.md 表 B 加弱断言），不在 dev-test-gen 内私自调整。

5. 生成 `output/result.sql`（按 `test-spec.md` §3.5 字面常量规则 + §3.5.1 运行时变量 + §3.5.2 间接约束断言）：
   - 字面值列**必须**带 `AS expect_<名>` 前缀；非字面列**不可**用 `expect_` 前缀。
   - **运行时引用统一用 `${var}` 占位**（封闭变量集，由 `util.AssertResultSQL` 替换；具体变量见 `test-spec.md` §3.5.1）。
   - 正常路径：按 `db_data.md` INSERT 步骤逐表生成"命中检查" SELECT。**详尽生成**——每一个写入字段都纳入断言。字段值五类（见 `test-spec.md` §3.5 详尽生成段）：
     - **业务字段**：从 03 yaml `requestBody.examples.<first>.value` 抽（与 `request.json` 一致），写 `AS expect_*`
     - **固定值**：从 `db_data.md` 字段口径段抽（业务恒定的字面值），写 `AS expect_*`
     - **运行时字段**：用 `${var}` 占位，写 `AS expect_*`
     - **约束类字段**：写在 WHERE 子句（见下方"间接约束断言"），不在 SELECT 列表 `expect_*`
     - **依赖 DB 默认的字段**：无期望值且无约束，不写入断言
   - 异常路径（`code ≠ 0`）：统一写 `SELECT 0 AS expect_count WHERE NOT EXISTS (SELECT 1 FROM <写入表>);` 校验未写入(详见 test-spec.md §3.5 计数类断言形态)。每个写入表一行。**不能写** `SELECT 0 AS expect_count FROM <表>;`——空表 SELECT...FROM 返 0 行,会被 runner 判为失败,跟"未写入应通过"语义反。
   - service-mock / repo-mock CASE：写占位 `SELECT 1 AS expect_noop;`（mock 函数不读 result.sql）。

   **间接约束断言**（约束类字段）：

   有结构约束（长度、非空、字符集）但每次值随机/动态的字段，无法写字面 `expect_*` 断言，改用 `WHERE` 子句限定：

   ```sql
   -- 示例骨架（具体表 / 字段 / 约束由各 API 的 db_data.md 决定）
   SELECT '${var-a}' AS expect_field_a,
          FALSE      AS expect_field_b
     FROM <写入表>
    WHERE <运行时关联条件>
      AND length(<约束字段>) >= <下限>;
   ```

   机制：若约束字段实际值不满足 WHERE 条件，过滤后 0 行 → runner 报 "returned 0 rows" → 该断言失败。

   约束类字段从 `db_data.md` 步骤说明中通过"长度 ≥ N"、"非空"、"字符集"等描述识别；逐 API 解析决定具体字段。

   **WHERE 子句不超 design 范围**（B1 防过度发挥）:WHERE 仅含 `db_data.md` 步骤明示的字段约束 + `test_case.md` 表 B 弱断言关联字段;**不**自由加 jsonb 字面比对 / 派生字段精确比对 / docs / 实装均未约定的字段。理由:超 design 范围的字段约束(如 PG jsonb cast text 格式)实装行为可能跟字面表达不一致(API-030 r2 真漂移教训)。如怀疑需校验未明示字段,改在 test_case.md 表 B 显式登记后再生成。

> **不覆盖已存在文件**：CASE 目录已存在则跳过该 CASE 的整组 fixture 生成，列入"不动清单"。

### Step 6: 生成代码骨架（薄外壳）

通用 helper 不在测试包内生成 —— 由 `ita/util/` 真包提供。本步骤只生成"调用 util 的薄外壳"。

1. **包名 / 文件名**（按 `test-spec.md` §4.1）：
   - 包名：`api<编号><operationid 全小写>`
   - 主文件：`TEST_DIR/<operationid_snake>_test.go`
   - **不生成** `helper_test.go`

2. **主文件顶部**（按 `test-spec.md` §4.6 参考样板）：
   - `import`：必含 `util` / `fxapp` / `infra` / `gin` / `fx`
   - 业务常量：
     - 端点 path **含 `{xxx}` 占位** → `const apiPathTemplate = "/v1/.../{xxx}/..."`（保留占位字面）
     - 端点 path **不含占位** → `const apiPath = "/v1/<具体>"`
     - 通用：`const apiMethod` / `testCallerPubkey`
   - 包级 `var truncateTables []string`（取 `db_data.md` 中**任何步骤**——含 SELECT / INSERT / UPDATE / DELETE——涉及的全部表，按依赖逆序；GET / 查询型接口仍按步骤 0 SELECT 涉及的表填入）
   - 包级 `var weakRules util.WeakRules`（按表 B **内部翻译表**翻译，见下；**map 字面值按 JSON path 字典序排列**）
   - 包级 `var testEngine` / `testDB` / `t0`
   - `TestMain(m *testing.M)`：依次调 `util.RequireTestEnv()` → `fxapp.NewServer(fx.Populate(&testEngine, &testDB))` + `app.Start(...)` → `m.Run()` → `app.Stop(...)`。**`fx.Populate` 仅注入 `testEngine` + `testDB` 两个目标**，不得额外注入包级变量
   - `caseDir(id) string` 工具函数

   **关键元素**清单见 `test-spec.md` §4.6；内部细节（import 顺序、注释、空行）不做约束。

3. **TestCASE_NNN 函数**（按层级生成,遵守顶部硬约束清单 §1-8）:

   - **`integration` 层** → §4.4 样板,每 CASE 函数体内直接调 `util.*` 6 步;path 占位用 `strings.Replace` 就地替换(硬约束 5,7)。
   - **`service-mock` / `repo-mock` 层**:
     - **就绪门**(同时满足): service 方法实装(`grep -r "func .* <operationId>(" internal/service/*.go` 命中) + `ita/mocks/<repo>_mock.go` 入库
     - 满足 → 构造 `ctrl := gomock.NewController(t)` + `mockRepo := mocks.NewMock<RepoName>(ctrl)`,按表 C 说明生成 `mockRepo.EXPECT().<Method>(...).Return(...)`,构造 service 实例断言 xerror code
     - 字面值规则:见硬约束清单 §3(DDL DEFAULT / `errors.New(<表 C 原文>)` / `.Return(...)`)
     - 多策略二选一(表 C 说明含 `/` 分隔) → **取最左侧**(防漂移)
     - **DoTx 接缝**: service 走 `*infra.ChannelClient.DoTx` 的 mock CASE → 注入真 `testDB` 作 `*infra.ChannelClient`(`fx.Populate` 装配),mock 仅打 repo 接口层
     - 不满足 → `t.Skip("TODO_MOCK: <表 C 说明列原文>")`,计入 TODO_MOCK
   - 函数头注释 `// <CASE-id>: <场景>`(场景从表 A 复制)

4. **`weakRules` 翻译与叶子字段名展开**：

   **步骤 a**：**Claude 语义解析** `test_case.md` 表 B「弱断言规则」列业务意图，输出端翻译为**封闭 mini-DSL** 之一：

   | 输出端 mini-DSL（封闭集合） | 语义 |
   |---|---|
   | `regex:<re>` | 字段值匹配正则 `<re>` |
   | `timestamp:near` | 字段为合理时间戳（接近测试执行时刻） |
   | `length-ge:<N>` | 字段字符串长度 ≥ `N` |
   | `prefix-join:<前缀>;<相对路径>` | 字段值 = 字面前缀 + 同响应内另一字段的值 |

   **表 B 写法识别示例**(不作枚举,Claude 按语义识别): 正则类 → `regex:`;时间类(含"时间" / "timestamp" 关键字) → `timestamp:near`;长度类 → `length-ge:`;前缀拼装类 → `prefix-join:`。解析不出 → `TODO_RULE`。

   > 输入端 Claude 容忍人类自然写法,输出端 mini-DSL 必须是上面 4 类之一(防漂移)。新增 mini-DSL 类型需先扩 `ita/util/AssertResponseBody` 实装。

   **步骤 b**：把表 B「字段」列的**叶子字段名**展开到响应里所有匹配的完整 JSON path（按 §4 §3.4 "叶子字段名" 约定）：

   - 取 Step 5.4 生成的正常路径 `response.json` 作为响应结构来源。
   - 遍历响应中的所有字段路径，找出叶子名等于表 B 字段名的 path（如表 B `channelId` → `data.metadata.channelId` + `data.memberList.0.channelId` 两条）。
   - 对每条匹配的 path 添加同一条 DSL 规则到 `weakRules`。
   - 表 B 字段名形如 `<对象>.<字段>`（含点路径片段）时，匹配末段相等且父级路径含该对象名的 path。
   - **数组索引展开规则**（防漂移）：响应中含数组的字段，按 Step 5.4 生成的 `response.json` 中**实际存在的索引**逐个展开——`response.json` 的 `memberList` 含 3 个元素 → 叶子名 `channelId` 展 `data.memberList.0.channelId` / `.1.channelId` / `.2.channelId` 三条；含 1 个 → 仅展 `.0.channelId` 一条。**不取**任意子集（如"只展 [0]" 或 "前 N"）。

   **步骤 c**：`prefix-join` 规则中的 `<field 全路径>` 引用项也按叶子名展开，但需指向响应根的相对路径（如表 B 写 `实际 channelId`，规则生成时取 `data.metadata.channelId` —— 即响应中首个 `channelId` 出现的路径）。

   **步骤 d**（防漂移）：输出 `weakRules` map 字面值时，**按 key 字符串字典序排列**。

> **不覆盖已存在函数**：若同名 `TestCASE_NNN` 已存在，跳过追加并列入"不动清单"。

### Step 7: 报告

向用户汇报。sub-agent 按以下三块自由组织,不必固定模板字段:

1. **指标**:CASE 数 / TODO_FIXTURE / TODO_RULE / TODO_MOCK / stop 触发 / mocks 同步 / `go vet` 结果
2. **关键决策点**:本次应用的 docs bug 修正、新建 / 跳过 / 多余 CASE、特殊 CASE 处理
3. **异常 / 文档质量警告**:`TODO_FIXTURE + TODO_RULE > 5`(TODO_MOCK 不计)触发警告,列具体未解析的 CASE / 字段;sub-agent 解析过程中发现的契约 / docs 冲突点

报告必须以 `## /dev-test-gen 报告 — <API-XXX>[ @case/<CASE-NNN>][ @sandbox/<标签>]` 起首,主会话据此聚合多 sub-agent 结果。

不执行测试,只生成文件。

## 注意事项

- **禁猜**：契约抽不出 / 列不齐时**立即停止并报错**，不得脑补字段值或层级。
- **不覆盖用户编辑**：CASE 目录或测试函数已存在时跳过该 CASE 的对应产物，并写入"跳过清单"。
- **不删除多余项**：S2/S3 多于 S1 的项不自动删除，列入报告由用户决策。
- **不执行测试 / 不动 git**：本命令只生成文件；测试执行、暂存、提交由用户在仓库根另行处理。
- **TODO 标记**：所有需要人工填的位置必须打标记（`TODO_FIXTURE` / `TODO_RULE` / `TODO_MOCK`），便于 `grep` 跟踪。
