# FileGate 配置字段依赖关系说明

本文梳理 `config.yaml` 中各配置块之间的**引用关系**、**条件依赖**与**运行期交叉依赖**。
行为依据为 `config/loader.go`（加载/校验）、`internal/engine/router.go`、`internal/server/handler.go`、
`internal/server/imgproxy.go`、`internal/engine/policy.go` 的实现。

## 0. 依赖总览（箭头 = "引用/依赖"）

```
namespaces[].class[]
   ├── backend_policy  ──引用──►  backend_policy[].name
   │                                   └── backends[]  ──引用──►  backends[].name
   ├── security
   │     ├── refer_check.enabled      ──依赖──► refer_check.allowed_referers（非空才有效）
   │     ├── signature.enabled        ──依赖──► signature.secret（必填）
   │     └── path_filter              （自包含，空配置 = 全部放行）
   ├── file_conversion.rules[]    ──引用──►  file_conversion_rules[].name
   │     ├── 规则选择：路径后缀 !rulename 或 query rule=；均未指定 → 不做转换
   │     ├── enable_request_params：对 file_conversion 内所有规则生效
   │     ├── 运行期 ──依赖──►  service.imgproxy.url（未配置则转换静默失效）
   │     └── service.imgproxy         ──依赖──►  system.server.base_url（生成 /origin/ 回源地址）
   └── response_headers               （独立，无依赖）

system.server.host/port  →  监听地址（main.go）
system.server.base_url    →  仅 imgproxy 链路使用；缺省时由 host:port 推导
```

## 1. 名称引用关系（启动时校验，`config/loader.go: validate`）

所有引用按 `NormalizeKey`（小写 + 去首尾空白）匹配，因此**配置中的名称大小写不敏感**。

| 字段 | 引用的目标 | 校验规则 |
|---|---|---|
| `namespaces[].backend_policy` | `backend_policy[].name` | 必填；引用不存在的策略 → 启动报错 |
| `backend_policy[].backends[]` | `backends[].name` | 至少一个后端可解析，否则报错；运行期若某个名字不存在，`OrderedBackends` 返回错误（502） |
| `class[].file_conversion.rules[]` | `file_conversion_rules[].name` | 每个条目必填且必须存在；同一 class 内不得重复引用，否则报错 |
| （无引用）`class[].security.*` | — | 三类安全策略彼此独立，可与上述任意组合 |

唯一性约束：`backends[].name`、`backend_policy[].name`、`file_conversion_rules[].name`、
`namespaces[].name` 全局唯一；`class[].name` 在 namespace 内唯一。
`namespaces[].name` + `class[].name` 直接决定对外 URL：`/fs/{namespace}/{class}/{objectPath}`。

## 2. backends：`type` 决定 `config` 子字段的适用性

| type | 必填子字段 | 可选子字段 | 字段间依赖 |
|---|---|---|---|
| `fs` | `config.root_path` | — | — |
| `s3` | `config.bucket` | `endpoint`、`region`、`access_key`、`secret_key` | `access_key` 与 `secret_key` 必须成对（都填或都不填） |
| `http` | `config.url_prefix` | `config.extra_headers` | — |

公共字段：
- `timeout`：≤0 时加载阶段重置为 5s；
- `circuit_breaker.*`：`failure_threshold > 0` 才会为该后端注册熔断器，否则请求直接放行；
- `retries` / `retry_delay`：**仅做了归一化，运行期未使用**（见 §7）。

## 3. backend_policy：策略字段间的依赖

- `strategy` 决定 `backends[]` 的语义：
  - `single`：只使用列表第一个；
  - `fallback` / `priority`：按配置顺序逐个尝试；
  - `round_robin`：跨请求轮转（进程内存态）；
  - `random`：随机排列；
  - 空值 → 按 `single` 处理；未识别值 → 运行时按 `fallback` 行为兜底（按顺序遍历）。
- `backends[]` 顺序即优先级顺序（对 fallback/priority 有实际意义）。
- 熔断器按**后端粒度**注册（`backends[].circuit_breaker`），策略本身不配置熔断。

## 4. class 内：security 三个子块的触发条件

| 子块 | 生效条件 | 依赖字段 | 说明 |
|---|---|---|---|
| `refer_check` | `enabled: true` | `allowed_referers` 非空才有实际放行能力 | 提取 Referer 域名后按域名匹配（支持 `*.example.com` 泛域名与单独 `*` 全放行）；未配置允许列表则所有请求 403 |
| `signature` | `enabled: true` | `secret` 必填（启动校验） | `expire` 用于限制 exp 的最大超前窗口（0 = 不限制） |
| `path_filter` | 始终生效（结构上始终存在） | 三者均为空 = 全部放行 | 校验顺序：`deny_patterns`（正则，命中即 403）→ `allow_paths`（非空时前缀必须命中）→ `allow_extensions`（非空时扩展名必须命中） |

安全策略与后端/转换**无依赖**：任何 class 组合都合法。注意 `/origin/` 回源接口**只执行
`path_filter`**，不执行 refer_check / signature——因此需要反向代理限制该接口的访问来源（代码注释已说明）。

## 5. file_conversion 链路：三层引用 + 运行期交叉依赖

```
class.file_conversion                             （类别级：可用规则白名单 + 参数开关）
    ├── rules[]    ──引用──►  file_conversion_rules[].name
    └── enable_request_params：query/后缀参数覆盖开关 + 范围限制
          （对整个 file_conversion 生效，rules 内所有规则共享）
          ├── width/height/quality：enabled 开关 + min/max 范围限制
          └── blur/format：bool 开关

file_conversion_rules[]                          （规则级：转换预设）
    ├── default_params：未指定参数时的兜底值（blur 为高斯模糊 sigma，浮点）
    ├── max_file_size：仅 imgproxy 链路使用（msfs 选项，启动时校验格式）
    └── watermark：enabled 时向 imgproxy 下发 wm: 选项
          （需要 imgproxy Pro 并在 imgproxy 端配置水印图；
           position/opacity 启动时校验）

规则选择（请求级）：路径后缀 !rulename 或 query rule=；
两者冲突 → 400；均未指定 → 不做转换，按原始路径直接回源。
```

**最重要的运行期依赖**：转换真正发生需要 `service.imgproxy.url` 非空。
- `service.imgproxy.url` 为空 → 客户端携带转换后缀/参数时，转换**静默失效**，直接按剥离后的
  `sourcePath` 从后端拉原文件（PathFilter 仍会校验该路径）；
- `service.imgproxy.url` 非空 → 转换请求走 imgproxy，此时还需要：
  1. `system.server.base_url` 非空且**对 imgproxy 实例可访问**——imgproxy 用它拼接
     `/origin/{namespace}/{class}/{sourcePath}` 回源（缺省时由 `host:port` 推导，可能不可达）；
  2. `service.imgproxy.timeout`：请求超时（<=0 时使用默认 20s）；
  3. `service.imgproxy.signature.enabled: true` 时，`key`/`salt` 必填（启动校验），
     且必须与 imgproxy 服务端配置一致（外部系统依赖）。

## 6. 系统级字段依赖

- `system.server.host` / `port`：进程监听地址（`main.go`），与路由无关；
- `system.server.base_url`：**仅 imgproxy 链路使用**；未配置时自动用 `http://{host}:{port}` 填充；
  若部署在反代/容器后，必须显式配置为外部可达地址，否则 imgproxy 回源失败（502）；
- `system.logging.access_log`：控制 Gin 访问日志；`system.logging.level` 已归一化但运行期未使用（§7）；
- `system.metrics.prometheus`：控制 `/metrics` 是否注册；`labels` 未使用（§7）。

## 7. 目前"解析但未生效"的字段（配置它们不会有任何运行期效果）

| 字段 | 现状 |
|---|---|
| `backends[].retries` / `retry_delay` | 仅加载时归一化，后端 `Fetch` 无重试逻辑 |
| `system.logging.level` | 归一化后未使用 |
| `system.metrics.labels` | 未注入 Prometheus 指标 |

注：`zip`（`default_params.zip` / `enable_request_params.zip`）已从配置结构**移除**——imgproxy
没有对应的 zip 处理选项，保留会造成"配置了没效果"的误导；`watermark` 现已实现（见 §5）。
`supported_formats` 也已移除——输出格式不再做白名单校验，仅受
`enable_request_params.format` 开关控制。

## 8. 常见配置错误对照（均会在启动时被拒绝）

| 配置错误 | 报错位置/信息 |
|---|---|
| 没有任何 `backends` | `at least one backend is required` |
| `namespace.backend_policy` 指向不存在的策略 | `references unknown backend policy` |
| `backend_policy.backends` 全部不可解析 | `has no resolvable backend` |
| `file_conversion.rules` 条目为空/引用不存在 | `file_conversion entry with empty rule` / `references unknown conversion rule` |
| 启用签名但 `secret` 为空 | `enables signature but secret is empty` |
| imgproxy 签名启用但 `url`/`key`/`salt` 缺失 | `imgproxy signature enabled but ...` |
| `base_url` 不是合法 URL | `invalid system.server.base_url` |
| s3 只填了 `access_key` 或 `secret_key` 之一 | `requires both access_key and secret_key` |
| 后端类型与必填 config 不符（如 http 缺 `url_prefix`） | 在 `server.New` 构建后端时报错 |
