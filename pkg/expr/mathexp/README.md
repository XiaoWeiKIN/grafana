# mathexp - 数学表达式执行引擎

## 概述

`mathexp` 是 Grafana 中用于表达式查询的数学表达式解析和执行引擎。它提供了对时间序列数据、数值集合和标量值进行数学运算、聚合和转换的能力。

## 核心功能

### 1. 数据类型

该模块支持以下核心数据类型:

- **Scalar (标量)**: 单个常量数值
- **Number (数值)**: 带标签的单个数值
- **Series (时间序列)**: 时间-数值对的序列
- **NoData (无数据)**: 表示无数据响应
- **TableData (表格数据)**: 单个表格数据框

所有类型都实现了 `Value` 接口,可以统一处理。

### 2. 表达式解析与执行

- **表达式树**: 通过 `parse` 子包将字符串表达式解析为抽象语法树(AST)
- **表达式执行**: `Expr` 结构体包装解析树,`State` 结构体维护变量和执行状态
- **操作支持**:
  - 一元运算: `-` (取反), `!` (逻辑非)
  - 二元运算: `+`, `-`, `*`, `/`, `**` (幂), `%` (取模)
  - 比较运算: `==`, `!=`, `>`, `<`, `>=`, `<=`
  - 逻辑运算: `&&` (逻辑与), `||` (逻辑或)

### 3. 内置函数

模块提供了丰富的内置数学函数:

- **数学运算**:
  - `abs()`: 绝对值
  - `log()`: 自然对数
  - `round()`: 四舍五入
  - `ceil()`: 向上取整
  - `floor()`: 向下取整

- **特殊值处理**:
  - `nan()`: 返回 NaN 值
  - `inf()`: 返回正无穷
  - `infn()`: 返回负无穷
  - `null()`: 返回空值
  - `is_nan()`: 检查是否为 NaN
  - `is_inf()`: 检查是否为无穷
  - `is_null()`: 检查是否为空值
  - `is_number()`: 检查是否为有效数值

### 4. 聚合函数 (Reduce)

支持对时间序列进行降维聚合:

- `sum`: 求和
- `mean`: 平均值
- `min`: 最小值
- `max`: 最大值
- `count`: 计数
- `last`: 最后一个值
- `median`: 中位数

**特殊处理模式**:
- `DropNonNumber`: 过滤掉 NaN 和 Inf 值
- `ReplaceNonNumberWithValue`: 将 NaN 和 Inf 替换为指定值

### 5. 重采样 (Resample)

提供时间序列重采样功能:

**上采样策略** (当新间隔小于原间隔):
- `pad`: 使用最后看到的值填充
- `backfilling`: 使用下一个值反向填充
- `fillna`: 填充为 null

**下采样策略** (当新间隔大于原间隔):
- 使用聚合函数 (`sum`, `mean`, `min`, `max`, `last`) 对区间内的值进行聚合

### 6. 联合操作 (Union)

在二元运算中,根据标签自动匹配和联合不同的时间序列或数值集:

- 支持标签精确匹配
- 支持标签包含关系匹配
- 自动处理标量与序列的广播运算
- 跟踪并报告被丢弃的不匹配项

## 架构设计

```
mathexp/
├── types.go          # 核心数据类型定义 (Scalar, Number, Series, etc.)
├── type_series.go    # Series 类型的实现和操作
├── exp.go            # 表达式执行引擎
├── funcs.go          # 内置函数实现
├── reduce.go         # 聚合函数实现
├── resample.go       # 重采样功能
├── parse/            # 表达式解析器
│   ├── parse.go      # 主解析器
│   ├── lex.go        # 词法分析器
│   └── node.go       # AST 节点定义
└── testing.go        # 测试工具
```

## parse 子包详解

`parse` 子包负责将字符串形式的数学表达式解析为抽象语法树(AST),是整个表达式引擎的基础。该包改编自 Go 语言标准库的 text/template 包。

### 架构组成

parse 包由三个主要组件构成:

#### 1. 词法分析器 (lex.go)

词法分析器将输入字符串分解为词法单元(token)。

**支持的 Token 类型**:
```go
- itemNumber      // 数字: 整数、浮点数、科学计数法、十六进制
- itemVar         // 变量: $A, $B, ${var_name}
- itemFunc        // 函数名: abs, log, sum 等
- itemString      // 字符串: "quoted string"
- 运算符:
  - itemPlus      // +
  - itemMinus     // -
  - itemMult      // *
  - itemDiv       // /
  - itemMod       // %
  - itemPow       // **
- 比较运算符:
  - itemEq        // ==
  - itemNotEq     // !=
  - itemGreater   // >
  - itemLess      // <
  - itemGreaterEq // >=
  - itemLessEq    // <=
- 逻辑运算符:
  - itemAnd       // &&
  - itemOr        // ||
  - itemNot       // !
- 分隔符:
  - itemLeftParen, itemRightParen  // ( )
  - itemComma     // ,
```

**工作原理**:
- 使用**状态机模式**进行词法分析
- 通过 goroutine 和 channel 实现流式处理
- 支持变量的两种语法: `$A` 和 `${var_name}`
- 自动忽略空白字符

**状态函数**:
- `lexItem`: 主状态,识别token类型并分派到相应状态
- `lexNumber`: 解析数字(支持十进制、十六进制、科学计数法)
- `lexFunc`: 解析函数名(字母和下划线)
- `lexVar`: 解析变量(支持 $ 前缀和 {} 包裹)
- `lexString`: 解析双引号字符串
- `lexSymbol`: 解析运算符和符号

#### 2. 语法解析器 (parse.go)

语法解析器将词法单元流转换为抽象语法树。

**文法定义** (基于运算符优先级递归下降):
```
O -> A {"||" A}                                          // 逻辑或 (最低优先级)
A -> C {"&&" C}                                          // 逻辑与
C -> P {("==" | "!=" | ">" | ">=" | "<" | "<=") P}      // 比较运算
P -> M {("+" | "-") M}                                   // 加减法
M -> E {("*" | "/" | "%") E}                             // 乘除模
E -> F {"**" F}                                          // 幂运算 (最高优先级)
F -> v | "(" O ")" | "!" F | "-" F                       // 因子 (一元运算、括号)
v -> number | func(...) | $var                           // 值
```

**解析方法**:
- `O()`: 解析逻辑或表达式
- `A()`: 解析逻辑与表达式
- `C()`: 解析比较表达式
- `P()`: 解析加减表达式
- `M()`: 解析乘除模表达式
- `E()`: 解析幂运算表达式
- `F()`: 解析因子(一元运算、括号、基本值)
- `v()`: 解析值(数字、函数、变量)

**核心特性**:
- **Lookahead机制**: 使用单 token 前瞻判断解析路径
- **类型检查**: 解析时进行类型检查,确保函数参数类型正确
- **错误恢复**: 使用 panic/recover 机制处理解析错误
- **函数验证**: 检查函数参数数量和类型匹配

#### 3. AST 节点 (node.go)

定义了构成抽象语法树的各种节点类型。

**节点接口**:
```go
type Node interface {
    Type() NodeType              // 节点类型
    String() string              // 字符串表示
    StringAST() string           // AST格式表示
    Position() Pos               // 源码位置
    Check(*Tree) error           // 类型检查
    Return() ReturnType          // 返回类型
}
```

**节点类型**:

1. **VarNode (变量节点)**
   - 表示查询变量引用 (如 `$A`, `$B`)
   - 存储变量名(去除 $ 和 {})
   - 返回类型: `TypeSeriesSet`

2. **ScalarNode (标量节点)**
   - 表示数字常量
   - 支持整数和浮点数
   - 解析时同时尝试 uint64 和 float64
   - 返回类型: `TypeScalar`

3. **StringNode (字符串节点)**
   - 表示字符串常量
   - 存储原始带引号文本和解析后文本
   - 返回类型: `TypeString`

4. **FuncNode (函数节点)**
   - 表示函数调用
   - 包含函数名、参数列表、函数定义
   - 执行类型检查:
     - 参数数量验证
     - 参数类型匹配
     - 支持变参类型(`TypeVariantSet`)
   - 支持动态返回类型(`VariantReturn`)

5. **BinaryNode (二元运算节点)**
   - 表示二元运算 (如 `A + B`, `A > B`)
   - 存储两个操作数和运算符
   - 返回类型: 两个操作数中优先级较高的类型
   - 格式化: 中缀表示法 `A op B`

6. **UnaryNode (一元运算节点)**
   - 表示一元运算 (如 `-A`, `!B`)
   - 存储操作数和运算符
   - 类型检查: 仅支持数值类型
   - 返回类型: 与操作数相同

**返回类型系统**:
```go
TypeString      // 字符串
TypeScalar      // 无标签标量
TypeNumberSet   // 带标签数值集合
TypeSeriesSet   // 带标签时间序列集合
TypeVariantSet  // 可变类型(Number/Series/Scalar之一)
TypeNoData      // 无数据
TypeTableData   // 表格数据
```

### 解析流程示例

对于表达式 `$A + $B * 2`:

1. **词法分析**:
   ```
   $A      -> itemVar("A")
   +       -> itemPlus
   $B      -> itemVar("B")
   *       -> itemMult
   2       -> itemNumber(2)
   ```

2. **语法解析** (根据优先级):
   ```
   P() 进入
   ├── M() -> Var("A")
   ├── itemPlus
   └── M() 进入
       ├── E() -> Var("B")
       ├── itemMult
       └── E() -> Scalar(2)
       返回 BinaryNode(*, B, 2)
   返回 BinaryNode(+, A, BinaryNode(*, B, 2))
   ```

3. **AST结构**:
   ```
   BinaryNode(+)
   ├── VarNode("A")
   └── BinaryNode(*)
       ├── VarNode("B")
       └── ScalarNode(2.0)
   ```

### 类型检查

解析器在构建 AST 时进行类型检查:

```go
// 函数类型检查示例
abs($A)  // ✓ TypeVariantSet 接受 Series
sum(5)   // ✗ sum 期望 SeriesSet,得到 Scalar
$A + "text"  // ✗ 不能对字符串进行算术运算
```

**检查规则**:
- 函数参数数量必须匹配
- 参数类型必须兼容
- `TypeVariantSet` 可接受 Number/Series/Scalar
- 一元运算仅支持数值类型
- 递归检查所有子节点

### 错误处理

**错误类型**:
1. **词法错误**: 非法字符、未闭合字符串、不完整变量
2. **语法错误**: 意外的 token、括号不匹配
3. **类型错误**: 参数类型不匹配、参数数量错误
4. **函数错误**: 未定义的函数

**错误报告**:
- 包含错误位置(字节偏移)
- 提供上下文信息
- 使用 panic/recover 终止解析

### 工具函数

**Walk**: 遍历 AST 树
```go
func Walk(n Node, f func(Node))
```

用途:
- 收集变量名
- 类型推断
- 代码生成
- 优化转换

### 扩展性设计

parse 包设计为可扩展:

1. **自定义函数**: 通过 `map[string]Func` 传入
2. **自定义类型检查**: `Func.Check` 回调
3. **变参返回**: `VariantReturn` 支持动态类型
4. **AST 转换**: 提供 `Walk` 遍历机制

### 性能特性

1. **流式处理**: lexer 使用 goroutine + channel
2. **单遍解析**: 一次遍历完成词法和语法分析
3. **零拷贝**: Token 通过 channel 传递
4. **懒惰求值**: 使用前瞻避免回溯

## 使用示例

### 创建和执行表达式

```go
// 解析表达式
expr, err := mathexp.New("$A + $B * 2")

// 准备变量
vars := mathexp.Vars{
    "A": seriesResultA,
    "B": seriesResultB,
}

// 执行表达式
results, err := expr.Execute("queryRefID", vars, tracer)
```

### Series 操作

```go
// 创建时间序列
series := mathexp.NewSeries("temp", labels, 100)

// 添加数据点
series.AppendPoint(time.Now(), &value)

// 聚合操作
number, err := series.Reduce("ref", mathexp.ReducerMean, nil)

// 重采样
resampled, err := series.Resample(
    "ref",
    time.Minute,           // 间隔
    mathexp.ReducerMean,   // 下采样方法
    mathexp.UpsamplerPad,  // 上采样方法
    from,
    to,
)
```

## 关键特性

1. **类型安全**: 所有值类型都基于 Grafana 的 `data.Frame` 结构
2. **标签感知**: 完整支持 Prometheus 风格的标签匹配
3. **NaN/Null 处理**: 提供多种策略处理特殊值
4. **性能优化**: 使用反射和类型断言实现灵活的函数调用
5. **错误追踪**: 集成 tracing 支持,便于调试
6. **通知机制**: 支持在结果中添加警告和提示信息

## 依赖

- `github.com/grafana/grafana-plugin-sdk-go/data`: Grafana 数据框架
- `github.com/grafana/dataplane/sdata`: 数据平面支持
- `github.com/grafana/grafana/pkg/infra/tracing`: 追踪支持

## 测试

模块包含完整的单元测试:
- `exp_test.go`: 表达式执行测试
- `funcs_test.go`: 函数测试
- `reduce_test.go`: 聚合测试
- `resample_test.go`: 重采样测试
- `types_test.go`: 类型测试
- 以及针对特殊情况的专项测试

## 注意事项

1. Series 必须有且仅有两个字段: 时间字段和数值字段
2. 时间字段不能包含 null 值
3. 二元运算会自动处理维度不匹配的情况,但会生成警告通知
4. 所有聚合函数在遇到 NaN 时的行为取决于是否使用了 `ReduceMapper`
