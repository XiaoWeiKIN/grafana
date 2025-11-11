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
