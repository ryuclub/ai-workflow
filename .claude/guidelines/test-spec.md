# API 测试规格契约

「测试代码 / fixture 机械生成」的**输入侧契约**。设计文档与产物目录必须满足本契约全部 schema，否则生成器拒绝生成。

## 1. 适用范围

- 设计源：`design/base/API/<API-XXX>-<operationId>/`
- 产物：`ita/<API-XXX>-<operationId>/`
- 任一目录不满足契约 → 生成器停机报错，**不得**绕过。

## 2. 设计侧契约（输入侧）

### 2.1 目录必备文件

每个 API 目录必须存在以下文件（缺一不可）：

| 文件 | 角色 | 主要内容 |
|------|------|---------|
| `API-<编号>.md` | 主心骨 | 上游引用、分层调用时序、错误码表、章节索引、已知 docs bug 修正规则 |
| `params_check.md` | 参数表 | 请求字段的 ID / 名 / 类型 / 必选 / min / max / binding tag |
| `db_data.md` | DB 表 | DB 移送步骤、事务边界、字段口径、特殊默认值说明 |
| `test_case.md` | **测试规格主表** | 用例表 / 断言口径表 / 测试层级表（详见 §2.2） |
| `questions.md` | 待确认项（允许为空） | 设计阶段未决问题；不影响生成 |

### 2.2 `test_case.md` 三张必须表

`test_case.md` 必须包含且只包含以下三个 H2 段，列顺序、列名、行内格式与下述 schema 完全一致。

#### 表 A：测试用例

```
## 测试用例

| 编号 | 场景 | 入力参数 | 期望响应 | 初始数据 | 备注 / 说明 |
| --- | --- | --- | --- | --- | --- |
| CASE-NNN | <一句话场景> | <见下：入参格式> | code=<数值>; <关键字段> | <见下：初始数据> | <可选说明> |
```

- 编号格式：`CASE-NNN`，三位数字、零填充、自 `CASE-001` 起连续递增、不得跳号。
- 一行 = 一个测试函数 = 一个 `CASE-NNN/` 目录。

**「入力参数」列**：业务可读的描述（自由文本 / 类 JSON 片段 / 占位等任意写法）。生成器**不解析其语义**，统一生成 `params.json = {}` 并在报告中标 `TODO_FIXTURE`，由人工对照本列补全 fixture。

**「期望响应」列**：必须以 `code=<整数>` 开头；其后允许任意业务描述（不参与机械抽取）。`code` 值用于生成 `expected.json` 的 `code` 字段；其它字段（含 `msg` / `data`）由人工补全。

**「初始数据」列**：本 CASE 跑测试**前**需要在 DB 中存在的状态。**推荐用业务自然语言描述**（"已存在一个频道；调用者是频道主；群内有 1 名管理员；邀请链接已生成"）；schema 风格（`channels(id=X), channel_members(role=owner)`）或裸 SQL 也可——生成器**不解析其语义**，列原文进 `data.sql` 头部 `TODO_FIXTURE` 注释，由人工补真实 SQL。**无需前置数据时留空，或写 `无`**。

#### 表 B：断言口径

```
## 断言口径

| 字段 | 弱断言规则 |
| --- | --- |
| <JSON path 或字段名> | <regex / range / 长度 / 前缀拼装等> |
```

未列入本表的字段一律按**全等比对**。本表只放需要弱断言的字段（生成的、时间相关的、随机的）。

**「弱断言规则」列**：作者按业务自然方式描述即可（`匹配正则 X` / `非零且为合理时间戳` / `长度 ≥ 10` / `前缀拼装 P + <field>` 等）——生成器（Claude）按语义解析意图，翻译为输出端封闭的 mini-DSL（`regex:` / `timestamp:near` / `length-ge:` / `prefix-join:` 之一）。无须背"固定句式"。

**空表 B（无弱断言字段）允许两种写法**——某些 API 响应不含时间戳 / 随机派生字段（如 `data={}` / `data={requireApproval:true}` 之类全静态响应），表 B 允许:

- 表格写法：保留 `| 字段 | 弱断言规则 |` 表头 + `| --- | --- |` 分隔行，无数据行
- 叙述写法：`## 断言口径` H2 段下直接写一段说明（如「响应仅含 code / msg / data.requireApproval，全部按全等比对，无时间戳 / 动态字段」），不写表格

生成器对两种写法等价处理：抽取出空字段集 → 输出 `weakRules = util.WeakRules{}` 空映射。

#### 表 C：测试层级

```
## 测试层级

| 用例 | 层级 | 说明 |
| --- | --- | --- |
| CASE-NNN[~CASE-MMM] | <integration|service-mock|repo-mock> | <见下：mock 注入语法> |
```

合法层级值（**生成器据此选择骨架**）：

| 层级值 | 含义 | 走线 |
|--------|------|------|
| `integration` | 真实 HTTP + 真实 DB | Controller → Service → Repo → PostgreSQL |
| `service-mock` | service 层 + mock repo | 直接构造 service，注入 mock 接口 |
| `repo-mock` | repo 层 + mock DB | gorm-mock / 内存 sqlite 等（按需启用） |

「用例」列允许 `CASE-001` 或 `CASE-001~CASE-007` 区间写法（含 `~` 前后空白），区间内所有编号必须连续。

**「层级」列**：作者按业务自然方式描述即可（`integration` / `集成测试` / `service-mock` / `service 层 mock` / `mock repo` 等任意写法）——生成器（Claude）按语义判定，映射到输出端**封闭三值** `{integration, service-mock, repo-mock}` 之一（骨架差异要求）。

**「说明」列**：业务可读的 mock 行为描述（自由文本，如「mock channels INSERT 抛错」/「mock SystemConfig 返回空」等）。生成器（Claude）按语义解析，结合 `internal/repo/` 接口签名生成 gomock EXPECT 链；输出严格遵守防漂移硬约束（字面值取 DDL DEFAULT / 错误用 `errors.New(<本列原文>)` / 一律 `.Return(...)` 不用 `.DoAndReturn` 等，详见 `dev-test-gen.md` Step 6.3）。

### 2.3 `params_check.md` schema

```
| ID | name | type | required | min | max | binding | remark |
```

- `ID`：`REQ-NNN`，三位数字、零填充、连续。
- `name`：使用点路径（例：`metadata.name`），与 OpenAPI `operationId` 的 body schema 对齐。
- `binding`：Go validator tag（如 `required,min=1,max=50`），生成器据此构造**边界值用例的 fixture**。

### 2.4 `db_data.md` schema

```
| # | 操作 | 表 | 关键字段 |
```

- `#`：从 0 起的步骤号（事务前置步骤可为 0）。
- 步骤说明部分允许自由文本，但**事务边界**必须单独成段并写明步骤号区间（例：「业务事务仅 2-6」）。
- 字段口径段（如默认值、依赖 DB DEFAULT 的列）按章节写明，不得散落到 test_case.md。

## 3. 产物侧契约（输出侧）

### 3.1 目录骨架

```
ita/<API-XXX>-<operationId>/
├── <operationId>_test.go        # 聚合测试文件（薄外壳：TestMain + TestCASE_NNN）
└── CASE-NNN/
    ├── input/
    │   ├── request.json         # 请求 body（HTTP 语义命名；即使为空对象也必须存在）
    │   └── data.sql             # 测试前置 SQL（无前置数据时写一行 `-- no seed`）
    └── output/
        ├── response.json        # 期望响应（HTTP 语义命名；含弱断言占位符）
        └── result.sql           # DB 状态断言 SQL（详见 §3.5）
```

- `CASE-NNN/` 目录数与 `test_case.md` 用例表行数**严格相等**。
- 四个 fixture 文件**全部必须存在**，即使为空也用占位（避免"找不到文件"和"忘记生成"的歧义）。
- 通用 helper（fixture 装载、HTTP runner、SQL 断言等）由 `ita/util/` 真包提供，**不在测试包内重写**——见 §4。

### 3.2 `input/request.json`

- 内容是请求 body 的 JSON（不含 header）。
- 空字段必须显式写 `""` / `null`，不可省略（让 fixture 自身可读，无须回查文档）。
- 不允许写注释 / trailing comma；标准 JSON。
- **数据来源**：
  - 正常路径（`code=0`）：从 03 yaml `requestBody.examples.<first>.value` 抽取，原样写入。
  - 异常路径：以正常路径为基线 + 按 `test_case.md` 表 A「入力参数」列做字段覆盖（参见 `dev-test-gen.md` Step 5.2 封闭覆盖模式）。
  - **字段覆盖语义**：在基线上修改指定字段值，**保留基线其他字段不变**（不是"只保留指定字段、删掉其他"）。
  - 表 A 写法未命中覆盖模式 → 写入基线 + 标 `TODO_FIXTURE`。

### 3.3 `input/data.sql`

- 测试前置 SQL，按顺序执行，失败即视为 fixture 错误。
- **数据来源**：原文复制自 `test_case.md` 表 A 当前 CASE 的「初始数据」列。
  - 列值为 `无` / 空 → `data.sql` 写单行 `-- no seed`。
  - 列值为业务描述（非空非 `无`） → `data.sql` 写：
    ```
    -- TODO_FIXTURE: <表 A 初始数据列原文>
    -- no seed
    ```
- 禁止 `DROP` / `TRUNCATE` 等破坏性语句；由 runner 在 CASE 之间做 schema 级清理。
- **CASE 自洽**：每个 CASE 的 `data.sql` 描述本 CASE 跑测试前的**全部** DB 状态（含系统级常驻行 + 业务前置），不依赖跨 CASE 的 baseline。`TruncateTables` 清空相关表后，`RunSeedSQL` 重建一切。

### 3.4 `output/response.json`

- 完整的期望响应 JSON（包含 `code` / `msg` / `data`）。
- 列入 `test_case.md` 断言口径表的字段允许写**占位值**（不参与全等比对，按弱断言规则匹配）；未列入者全等比对。
- **数据来源**：
  - 正常路径（`code=0`）：从 03 yaml `responses.'200'.examples.<first>.value` 抽取，展开 YAML anchor / alias 后输出 JSON。
  - **业务字段反映 request 覆盖**：响应中由请求 schema 定义的可写字段（来自 03 yaml `requestBody` 引用的 schema 字段集）取自 `request.json` 的最终值（含字段覆盖结果）；非可写 / 派生 / 服务端生成字段按 03 example **原样保留**。
  - **严格 03 yaml 抽取（非可写字段）**：除"已知 docs bug 修正规则"覆盖的字段外，派生字段字面值原样保留；不做业务判断或默认化。
  - 已知 03 yaml example 与业务实际不符的字段，按 `API-<编号>.md` 中显式声明的"已知 docs bug 修正规则"覆盖。具体条目由各 `API-<编号>.md` 自行登记，本契约不内置示例条目。
  - 异常路径（`code≠0`）：模板 `{"code":<N>, "msg":"<04 短文案>", "data":null}`。`<04 短文案>` = 04 文档错误码表中对应 code 的方括号字面值去括号（如 `[参数校验失败]` → `"参数校验失败"`）。
- 派生字段（时间戳 / 派生 id / caller pubkey / 服务端默认值等）的字面值保留 03 yaml example 中的样本；弱断言规则（test_case.md 表 B）保证全等比对不卡死。
- 实装运行后若发现派生字段实际值与 03 example 不一致，**不在 dev-test-gen 内私自调整**：在 `API-<编号>.md` 补一条修正规则，或扩 test_case.md 表 B 弱断言。

### 3.5 `output/result.sql`

若干 `SELECT` 语句；runner 按下列规则对每条断言。

**字面常量识别**：

- 仅 SELECT 列表中 `AS expect_<名>` 别名标记的字面值列参与"首行全等"断言。
- 字面值仅指：字符串字面量 `'...'`、整数 / 浮点、布尔 `TRUE`/`FALSE`、`NULL`。
- 其它列（列引用、表达式、子查询、聚合）不参与断言，仅供 SQL 自身的 `WHERE` / `JOIN` 约束。
- 别名前缀 `expect_` 是机械识别标记；不带此前缀的别名一律不参与断言。

**断言形态**：

- 命中类（正常路径）：返回 ≥ 1 行，首行所有 `expect_*` 列等于其字面值。
- 计数类（异常路径，未写入断言）：`SELECT 0 AS expect_count WHERE NOT EXISTS (SELECT 1 FROM <写入表>);` —— 表空 → WHERE 命中 → 返 1 行 `expect_count=0` 通过；表非空 → WHERE 不命中 → 返 0 行 → runner 报 fail。**不能写** `SELECT 0 AS expect_count FROM <表>;`,因为 `util.AssertResultSQL` 把"0 行"判为失败,空表 `SELECT...FROM` 必返 0 行,跟"未写入应通过"语义反。

**异常路径 UPDATE-only 流程的例外**：

INSERT 流程的异常断言「truncate 后整表 0 行 = 未写入」用上方 `SELECT 0 AS expect_count WHERE NOT EXISTS (...)` 模板;但 **UPDATE-only** 流程(如 API-002 updateChannel / API-033 updateMyAlias)异常 CASE 通常需要 seed 一行被更新的目标,此时仍需检查"行字段保持原值",改写为「seed 行的关键字段保持原值」断言:

```sql
-- 示例骨架（具体字段由各 API 的 db_data.md 决定）
SELECT '<seed 原值>' AS expect_field_a,
       '<seed 原值>' AS expect_field_b
  FROM <UPDATE 目标表>
 WHERE <运行时关联条件，定位 seed 行>;
```

机制：异常时事务回滚 / 不进入 UPDATE，行字段保持 seed 原值 → 断言通过；若实装错误地更新了行，字段值变化 → 断言失败。同步写入的审计表（如 `channel_audit_logs`）若 seed 时为空，仍可用 `SELECT 0 AS expect_count WHERE NOT EXISTS (SELECT 1 FROM channel_audit_logs);` 做"未写审计"断言（审计表 truncate 后真空）。

**生成器自动改写**（不再留 TODO 给作者）：UPDATE-only 流程异常 CASE，生成器**直接读 `input/data.sql` 的 INSERT 字段值**作 seed 原值断言写入 `result.sql`。同时对审计表 / INSERT 不涉及的表保留 `SELECT 0 AS expect_count` 断言（truncate 后真空）。两种断言可在同一 result.sql 共存：

```sql
-- UPDATE 目标表：seed 原值不变（事务回滚 / 不进入 UPDATE）
SELECT '摄影爱好者（重启版）' AS expect_name,
       '新公告：每周日晚作品评选' AS expect_announcement
  FROM channels
 WHERE id = '${channelId}';

-- 审计表：未写入（truncate 后真空）
SELECT 0 AS expect_count WHERE NOT EXISTS (SELECT 1 FROM channel_audit_logs);
```

例外情况:如果 `input/data.sql` 本身含 `TODO_FIXTURE`（初始数据列业务描述无法解析），`result.sql` 对应字段也保留 `TODO_FIXTURE` 联动——seed 原值未知，无法生成断言。

**SQL 语法限制**：

- 不允许 `\` 续行 / psql meta 命令；纯 SQL。
- 语句以 `;` 结尾，runner 按 `;` 切分；语句间允许空行 / `--` 行注释。
- 不用 `/* */` 块注释。

**详尽生成（正常路径）**：按 `db_data.md` INSERT 步骤逐表生成命中检查；每一个写入字段都纳入断言。字段分五类：

| 类别 | 来源 | 落点 |
|---|---|---|
| 固定值 | `db_data.md` 字段口径段（业务恒定值） | `AS expect_*` 字面常量 |
| 业务字段 | 03 yaml example（与 `request.json` 一致） | `AS expect_*` |
| 运行时字段 | `${var}` 占位（封闭集合见 §3.5.1） | `AS expect_*` |
| 约束类字段 | 有约束但值随机 / 动态 | WHERE 子句（见 §3.5.2），不在 SELECT |
| 依赖 DB 默认 | 自增 id / created_at 等 | 不写入断言 |

#### 3.5.1 运行时变量（封闭集合）

`result.sql` 允许 `${var}` 占位，runner 按 `;` 切分**之后**、每条语句执行**之前**做**纯文本**替换。**变量集合封闭**，不在表内的占位视为字面字符串、不替换。

下表为本项目封闭集合（其它项目按需配置 util 实现，并在此表登记）：

| 变量名 | 类型 | 来源 |
|---|---|---|
| `${callerPubkey}` | string | 测试函数注入的 caller pubkey |
| `${channelId}` | string | 上一个 `callAPI` 响应解析出的 `data.metadata.channelId`；service-mock 层级不可用 |
| `${apiPath}` | string | 端点路径常量（如 `"POST /v1/channels"`） |

- 字符串变量替换**不加引号**——SQL 模板写 `'${callerPubkey}'`（模板设计者自带单引号）。
- 派生 id 类变量（如 `${channelId}`）在 service-mock 测试中不可用时，runner 跳过该 SQL（mock 层级不读 result.sql，见 §4.5）。
- 新增变量须先在本表登记，生成器不得自创。

#### 3.5.2 间接约束断言（WHERE 子句模式）

约束类字段（有结构约束但值随机 / 动态）用 WHERE 子句限定。机制：约束不满足 → WHERE 过滤后 0 行 → runner 报 "returned 0 rows" → 该断言失败。

```sql
SELECT '${var-a}' AS expect_field_a,
       FALSE      AS expect_field_b
  FROM <写入表>
 WHERE <运行时关联条件>
   AND length(<约束字段>) >= <下限>;
```

约束类字段从 `db_data.md` 步骤说明中通过"长度 ≥ N" / "非空" / "字符集" 等描述识别；逐 API 解析决定具体字段。

## 4. 代码侧契约

通用 helper（fixture IO / HTTP / SQL / 断言）由 **`ita/util/` 真包**提供。测试包内**只生成薄外壳** —— TestMain + 包级常量 + `TestCASE_NNN`，每个函数体 ≤ 20 行；所有断言 / IO / 解析逻辑都走 util 导出 API。

> **漂移判断口径**：多次生成产物之间的"漂移"看以下指标，**不强求**逐行 diff 一致：
> - fixture 文件内容（合法 JSON / SQL）一致
> - `TestCASE_NNN` 函数集合与 `test_case.md` 表 A 一致（数量、命名）
> - 测试函数对 util API 的**调用关系**一致（哪个 helper / 参数语义）
> - util 包级 `truncateTables` / `weakRules` 等常量的关键内容一致
>
> 不在判断范围内：import 顺序、注释文案、变量声明位置、错误信息字面、空行排版。

### 4.1 包名与文件（硬约束）

- 测试包名：`api<编号>{operationid}`，全小写、无分隔符（例：`api001createchannel`）。
- 主测试文件：`<operationId>_test.go`（如 `create_channel_test.go`）。一个 API 一个文件，TestMain + 所有 `TestCASE_NNN` 聚合于此。
- **不写** `helper_test.go` —— 通用工具在 `ita/util/`。
- 业务包级常量（端点 / 测试 pubkey / 表清单 / weakRules）放主测试文件顶部。

### 4.2 测试函数命名（硬约束）

- 格式：`TestCASE_NNN`（与 `test_case.md` 表 A 编号 1:1，下划线分隔，三位零填充）。
- 函数体不写业务断言字面值；常量来自 fixture 文件 + util 函数返回。

### 4.3 util 包导出 API（参考清单）

测试代码经由以下导出函数完成所有重活：

```go
// runner.go —— 测试执行驱动
func LoadRequest(t *testing.T, caseDir string) []byte
func LoadResponse(t *testing.T, caseDir string) []byte
func RunSeedSQL(t *testing.T, db *sql.DB, caseDir string)
func TruncateTables(t *testing.T, db *sql.DB, tables []string)
func CallAPI(t *testing.T, engine *gin.Engine, method, path string,
             headers map[string]string, body []byte) (int, []byte)

// assert.go —— 所有断言 / 解析
type WeakRules = map[string]string
func AssertResponseBody(t *testing.T, got, expected []byte, rules WeakRules, t0 time.Time)
func AssertResultSQL(t *testing.T, db *sql.DB, caseDir string, vars map[string]string)
func LookupJSONString(t *testing.T, body []byte, path string) string
func LookupJSONValue(t *testing.T, body []byte, path string) any

// setup.go —— 启动期：必备环境检查；缺值时自动 provision 测试期依赖（容器、SDK stub 等）
var DefaultRequiredEnvVars []string
func RequireTestEnv(required ...string)
```

弱断言 mini-DSL（封闭集合，由 `AssertResponseBody` 识别；新规则须先改 util 包再用）：

| 前缀 | 语义 |
|------|------|
| `regex:<re>` | 字段值匹配 Go regexp |
| `length-ge:<N>` | 字符串长度 ≥ N |
| `timestamp:near` | int64 毫秒；`t0 ≤ v ≤ now()+1s` |
| `prefix-join:<前缀>;<另一字段 JSON path>` | 字段值 = 前缀 + 引用字段实际值（`;` 分隔，避开 markdown 表格 `\|`） |

`result.sql` 中 `${var}` 占位的封闭变量集见 §3.5.1；新增变量须先在该表登记。

### 4.4 集成版骨架（`integration`）—— 参考样板

调用关系是约束，具体写法不强求。每个 `TestCASE_NNN` 大致：

```go
// CASE-NNN: <场景，从 test_case.md 表 A 复制>
func TestCASE_NNN(t *testing.T) {
    util.TruncateTables(t, testDB.Db, truncateTables)
    util.RunSeedSQL(t, testDB.Db, caseDir("CASE-NNN"))

    headers := map[string]string{"pubkey": testCallerPubkey}
    status, resp := util.CallAPI(t, testEngine, apiMethod, apiPath,
        headers, util.LoadRequest(t, caseDir("CASE-NNN")))
    if status != 200 {
        t.Fatalf("HTTP status=%d body=%s", status, string(resp))
    }
    util.AssertResponseBody(t, resp, util.LoadResponse(t, caseDir("CASE-NNN")), weakRules, t0)
    util.AssertResultSQL(t, testDB.Db, caseDir("CASE-NNN"), map[string]string{
        "callerPubkey": testCallerPubkey,
        // 派生 id 类变量从响应解析；具体 JSON path 取决于 API 响应结构
        // 例（API-001 createChannel）："channelId": util.LookupJSONString(t, resp, "data.metadata.channelId"),
        "apiPath":      apiMethod + " " + apiPath,
    })
}
```

**关键调用关系**（漂移检测项）：

1. truncate → seed → callAPI → status 检查 → 响应断言 → 结果 SQL 断言（顺序固定）
2. 装配 vars 时派生 id 类变量取自 `LookupJSONString` 的响应解析值（具体 path 因 API 而异）
3. callerPubkey 由测试函数传入，不在 SQL 模板内硬写

**结构硬约束（防漂移）**：

- **禁止抽自定义辅助函数**：每个 `TestCASE_NNN` 函数体内**直接**调 `util.*`，**不得**抽 `runIntegrationCase` / `resolvedPath` 等项目自定义 helper。
- **path 占位处理**：端点路径含 `{xxx}` 占位（path param）时，包级常量 `apiPathTemplate = "/v1/.../{xxx}/..."`；CASE 函数体内**就地** `strings.Replace(apiPathTemplate, "{xxx}", <CASE 用值>, 1)`，赋给本地变量后再调 `CallAPI`。不含 path param 时常量名 `apiPath = "<具体>"`。
- **`weakRules` map 字面值排序**：按 JSON path **字典序**（key 字符串比较）输出。map 本身无序，但写在源码时按字典序最稳定。

### 4.5 service-mock 版骨架（`service-mock`）—— 参考样板

```go
// CASE-NNN: <场景>
func TestCASE_NNN(t *testing.T) {
    // TODO_MOCK: <test_case.md 表 C「说明」列原文>
    t.Skip("TODO_MOCK: 待 impl 阶段确定 repo 接口后补 mock setup")
}
```

**生成器边界（按层级区分）**：

- `integration` 层级：dev-test-gen **不读** `internal/repo/` / `internal/service/` 等 impl 代码（测试代码不耦合实装细节）。
- `service-mock` / `repo-mock` 层级：层级声明本身即放行——mock CASE 必然需要 impl 接口，dev-test-gen 读取对应实装包（`internal/repo/<api>.go` / `internal/service/<api>.go`）的导出接口 + 方法签名，结合表 C 说明列描述的 mock 行为，自动生成 `<mock>.EXPECT().<Method>(...).Return(...)` 调用（gomock 风格）。
- 若实装包尚未存在 / 接口未定义，**降级**为 `t.Skip("TODO_MOCK: ...")`，等 impl 完成后重跑 dev-test-gen 自动补齐。

**Mock 库**：`go.uber.org/mock/gomock`。Repo 接口由 `mockgen` 生成至 `ita/mocks/<repo>_mock.go`，全部 `//go:generate mockgen ...` 指令集中在 `ita/mocks/generate.go`（**不动** `internal/repo/` 接口文件）。

**`ita/mocks/` 目录由 `/dev-test-gen` 自动管理**（见 dev-test-gen.md Step 1.5）：
- 每次调用 `/dev-test-gen` 都扫 `internal/repo/` 当前接口集，**自动**重写 `generate.go`、删除孤儿 mock、跑 `go generate`
- 开发者改接口签名 → 跑 `/dev-test-gen` → mocks 自动跟上；不需手动 `go generate`
- 兜底：CI 仍跑 `go generate ./ita/mocks/... && git diff --exit-code`，防有人绕过 `/dev-test-gen` 直改接口

mock 实装参考样板（impl 阶段确定 repo 接口后由 dev-test-gen 自动生成，或人工补）：

```go
ctrl := gomock.NewController(t)
mockRepo := mocks.NewMockChannelRepository(ctrl)
mockRepo.EXPECT().<Method>(gomock.Any(), gomock.Any()).Return(<spec>)  // 按表 C 说明
// 多次调用用 .Times(N) / .AnyTimes()；未设置 EXPECT 的方法被调用会自动 fail
//（等价于「无 INSERT / 不应被调用」断言，不需额外计数器）。
svc := service.NewChannel(mockRepo, ...)
_, err := svc.CreateChannel(ctx, req, callerPubkey)
xerr := err.(*xerror.Error)
if xerr.Code != <expected_code> { t.Fatal(...) }
```

mock CASE 仍要求 `input/request.json` 等四件套存在（按 §3.1），但 `data.sql` / `result.sql` 写占位（`-- no seed` / 单条 noop SELECT），mock 函数体内不读。

**字面值确定性（防漂移硬约束）**：

- **Return 值含模型字段时，字面常量取自 DDL 真相源**：mock CASE 构造 `*modelgorm.<X>` 等 Return 值，字段字面值优先取 06-表定义.md 对应列的 `DEFAULT` 子句；DDL 无 DEFAULT 的字段取业务最小合法值（如 `MemberLimit: 1`）。**禁止** AI 自创默认值（如 `MemberLimit: 200` / `1000` 等随机数）。
- **Mock 抛错统一用 `errors.New(<原文>)`**：错误消息字面值=`test_case.md` 表 C「说明」列原文中描述错误源的短语（去引号、去多余空白）；**禁止**改写为 `"simulated DB write failure"` / `"mock channels INSERT failed"` 等同义不同字面的表述。
- **Mock 返回一律用 `.Return(...)` 直接形式**：固定值 / 固定错误的 mock 不允许用 `.DoAndReturn(func)` 包装；仅当返回值需要从入参动态计算时才用 `.DoAndReturn`。
- **多策略二选一取最左侧**：表 C「说明」列含 `/` 分隔多策略（如"返回空 / 抛错"）→ 一律取最左侧策略，避免轮次间随机选择。
- **DoTx 接缝**：service 内部走 `*infra.ChannelClient.DoTx` 跨表事务的 mock CASE，允许**注入真 `testDB`** 作 `*infra.ChannelClient` 参数（已由 `fx.Populate` 装配）；mock 仅打 repo 接口层，不要为 `*infra.ChannelClient` 单独 mock（该类型不在 `ita/mocks/` 集合）。

### 4.6 主测试文件顶部模板 —— 参考样板

`<operationId>_test.go` 顶部组织（具体写法不强求，**关键元素**见后列）：

```go
package api<编号><operationid 全小写>  // 例: api001createchannel

import (
    "context"
    "fmt"
    "os"
    "testing"
    "time"

    "github.com/gin-gonic/gin"
    "go.uber.org/fx"

    fxapp "mosavi.space/mosavi-channel-service/internal/app"
    "mosavi.space/mosavi-channel-service/internal/infra"
    "mosavi.space/mosavi-channel-service/ita/util"
)

// 业务常量（由生成器从 API-<编号>.md 抽取）
const (
    apiMethod        = "<HTTP-METHOD>"        // 由 API-<编号>.md 端点声明决定
    apiPath          = "/<v1>/<path>"         // 同上
    // testCallerPubkey 必须为真实 64 HEX 字面值（取 03 yaml example caller pubkey；
    // 多角色 CASE 用本地 testAdminPubkey / testMemberPubkey 等本地常量）。
    // 严禁用 "TODO_FIXTURE_..." 占位字符串 —— 落库需有效 pubkey 才能跑通 integration。
    testCallerPubkey = "<03 yaml example caller pubkey 字面 HEX 64 字符>"
)

// 表清理清单（由生成器从 db_data.md INSERT 步骤逆序抽取）
var truncateTables = []string{
    // <按 db_data.md INSERT 步骤逆序逐行列出业务表，外键依赖侧先清>
    // 例（API-001 createChannel）:
    //   "channel_audit_logs", "channel_invite_links",
    //   "channel_member_traits", "channel_members", "channels",
}

// 弱断言规则（由生成器从 test_case.md 表 B 翻译为 DSL）
var weakRules = util.WeakRules{
    // <由生成器按表 B 翻译 + 叶子字段名展开到完整 JSON path>
}

// 包级共享 —— fx 装配后注入
var (
    testEngine *gin.Engine
    testDB     *infra.ChannelClient
    t0         time.Time
)

func TestMain(m *testing.M) {
    t0 = time.Now()
    util.RequireTestEnv()

    app := fxapp.NewServer(
        fx.Populate(&testEngine, &testDB),
        fx.NopLogger,
    )
    if err := app.Start(context.Background()); err != nil {
        fmt.Fprintf(os.Stderr, "fx start: %v\n", err)
        os.Exit(1)
    }
    code := m.Run()
    _ = app.Stop(context.Background())
    os.Exit(code)
}

func caseDir(id string) string { return "./" + id }
```

**关键元素**（漂移检测项）：

| # | 元素 | 必须项 |
|---|---|---|
| 1 | `const apiMethod` / `apiPath` 或 `apiPathTemplate` | 来自 `API-<编号>.md` 端点声明。含 `{xxx}` 占位 → `apiPathTemplate`；不含 → `apiPath` |
| 2 | `var truncateTables` | 取 `db_data.md` 中**任何步骤**（含 SELECT / INSERT / UPDATE / DELETE）涉及的全部表（含系统级常驻表如 `system_config`），按依赖逆序排列（子表先 / 父表后）。GET / 查询型接口虽无业务 INSERT，仍按其步骤 0 SELECT 涉及的表填入 |
| 3 | `var weakRules` | util.WeakRules 类型，按 `test_case.md` 表 B 翻译；可为空 |
| 4 | `TestMain` 内调用 `util.RequireTestEnv()` 在 `fxapp.NewServer` 之前 | 顺序 |
| 5 | `fx.Populate(&testEngine, &testDB)` 装配 | **仅**这两个目标，不得多注入其它包级变量 |
| 6 | `caseDir(id) string` 返回 `"./" + id` | 工作目录约定 |

不限定：`import` 顺序、错误打印格式、变量声明的具体位置、注释文案、空行排版。

### 4.7 测试环境配置（硬约束）

测试期 DB 配置通过环境变量注入（变量名由项目侧约定，util 实现按项目配置）。

- `TestMain` 起始处调用 `util.RequireTestEnv()`：
  - 全部必需变量已设 → 用现有 DB（"自备 PG"，开发者负责 schema 准备）
  - 任一变量缺失 → util 自动起共享 testcontainers PG 容器（跨进程 `Reuse: true`），并把连接信息写入 env
- 每个 CASE 自洽（见 §3.3）——util 不灌任何 baseline，DB 状态完全由 CASE `data.sql` 控制。

## 5. 校验

### 5.1 三方一致性

对任意 API 目录，下列三个集合必须严格相等：

```
S1 = test_case.md 表 A 的 CASE 编号集合
S2 = ita/<API>/CASE-NNN/ 目录名集合
S3 = ita/<API>/*_test.go 中 TestCASE_NNN 函数名集合
```

生成器（Claude）按 test_case.md CASE id 列表迭代产出 fixture + 测试函数，产物天然自洽。Step 7 报告里列「新建 / 跳过 / 多余」三方差异供 review。

### 5.2 fixture 完整性

每个 `CASE-NNN/` 必须满足：

- `input/request.json` 存在且为合法 JSON
- `input/data.sql` 存在（允许 `-- no seed`）
- `output/response.json` 存在且为合法 JSON，含 `code` 字段
- `output/result.sql` 存在（异常路径必须存在校验"未写入"的 SELECT）
