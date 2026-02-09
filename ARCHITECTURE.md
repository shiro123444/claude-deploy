# Claude Relay 架构深度分析

本文档记录了 VS Code Claude Agent 第三方中继适配过程中的关键发现、踩坑经验和架构决策，供后续开发者和 AI 助手参考。

## 目录

- [核心架构：三层请求流水线](#核心架构三层请求流水线)
- [与官方说法的对照](#与官方说法的对照)
- [关键教训：cli.js 才是真正的 API 调用者](#关键教训clijs-才是真正的-api-调用者)
- [cli.js 补丁的坑](#clijs-补丁的坑)
- [cli.js 函数签名发现方法](#clijs-函数签名发现方法)
- [模型 ID 映射机制](#模型-id-映射机制)
- [常见问题排查](#常见问题排查)
- [版本升级注意事项](#版本升级注意事项)

---

## 核心架构：三层请求流水线

VS Code 的 Claude Agent 模式有 **三层**，不是简单的两层：

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                    VS Code Extension Host (extension.js)                      │
│                                                                               │
│  ┌─────────────────────────┐    ┌──────────────────────────────────────────┐  │
│  │  ClaudeCodeAgent        │    │  ClaudeLanguageModelServer (HTTP 代理)    │  │
│  │  ├── UI 面板渲染         │    │  ├── 监听 localhost:{port}               │  │
│  │  ├── 模型选择器          │    │  ├── selectEndpoint() 模型 ID 映射       │  │
│  │  ├── 消息显示            │    │  ├── 转发到 VS Code Language Model API   │  │
│  │  └── 工具调用 UI         │    │  └── 最终到达 CAPI / 第三方 Relay        │  │
│  └──────────┬──────────────┘    └──────────────────┬───────────────────────┘  │
│             │  spawn()                              ▲                          │
│             │  ANTHROPIC_BASE_URL=localhost:{port}   │  HTTP 请求               │
│             ▼                                       │                          │
│  ┌──────────────────────────────────────────────────┴──────────────────────┐  │
│  │  @anthropic-ai/claude-agent-sdk (嵌入在 extension.js 中的 SDK 代码)     │  │
│  │  ├── query() 入口函数                                                   │  │
│  │  ├── ESe 类: 子进程管理器                                                │  │
│  │  ├── spawn(node, [cli.js, --output-format, stream-json, ...])          │  │
│  │  └── stdio 通信 (stdin/stdout JSON stream, stderr 日志)                 │  │
│  └──────────────────────────┬─────────────────────────────────────────────┘  │
│                             │  child_process.spawn()                          │
└─────────────────────────────┼─────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│                    CLI 子进程 (cli.js, 11MB, Node.js ES Module)                │
│                                                                               │
│  ├── 构建 Anthropic SDK 客户端 (nH 函数)                                      │
│  ├── 发送 HTTP 请求到 ANTHROPIC_BASE_URL (→ localhost 代理)                    │
│  ├── 流式 SSE 解析 (g81 async generator)                                      │
│  ├── 模型名 ANSI 清理 (Gu 函数)                                               │
│  ├── MCP 服务器连接管理                                                        │
│  └── 通过 stdio 与 SDK 通信                                                   │
│                                                                               │
│  补丁方式: globalThis 函数入口注入                                              │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 完整请求流程

1. **用户输入** → ClaudeCodeAgent 将消息放入 `_promptQueue`
2. **SDK query()** 消费 `_createPromptIterable()` 生成的消息
3. **SDK ESe.spawn()** 以 `child_process.spawn(node, [cli.js, ...])` 启动子进程
   - 传入环境变量: `ANTHROPIC_BASE_URL=http://localhost:{port}`, `ANTHROPIC_API_KEY={nonce}`
4. **cli.js 子进程** 构建 Anthropic SDK 客户端，发送 HTTP 请求
   - 请求发往 `http://localhost:{port}` (不是直接发到中继站！)
5. **ClaudeLanguageModelServer** 接收请求:
   - `selectEndpoint()` 做模型 ID 映射 (如 `claude-sonnet-4-20250514` → `claude-sonnet-4.20250514`)
   - `requestBody.model = selectedEndpoint.model` 替换请求体中的模型
   - 转发到 VS Code Language Model API → 最终到 CAPI 或第三方 Relay
6. **响应** 原路返回: CAPI → 代理 → cli.js → SDK → ClaudeCodeAgent → UI

### 关键洞察

- **cli.js 不直接连接中继站** — 它连接的是 `localhost` 上的代理服务器
- **代理服务器 (ClaudeLanguageModelServer)** 才是连接真实 API 的中间人
- **模型 ID 映射发生在两个地方**:
  1. cli.js 内部构建请求时 (我们的 `globalThis.__cliMap` 补丁)
  2. 代理服务器的 `selectEndpoint()` (extension.js 中的 `__mapModel` 补丁)
- SDK 代码嵌入在 extension.js 中（约在字节偏移 1.37M-1.58M），但它 spawn 的 **cli.js 是独立进程**

## 与官方说法的对照

### 官方说法

官方描述说 "Claude Agent SDK 中并不存在独立的 cli.js 进程"，SDK 通过 `import('@anthropic-ai/claude-agent-sdk')` 后调用 `query()` 函数运作。

### 实际验证（extension.js 编译产物分析）

通过阅读编译后的 extension.js (copilot-chat-0.37.1) 源码，发现：

**1. SDK 确实是 in-process 导入（这部分官方正确）**：
```javascript
// 字节偏移 10803760
tTe=class{async query(e){
  let{query:t}=await Promise.resolve().then(()=>(mZt(),pZt)); // SDK 模块
  return t(e);
}};
```

**2. 但 SDK 内部确实 spawn 了 cli.js 子进程（官方遗漏的部分）**：
```javascript
// 字节偏移 1429514: mZt() 加载 child_process 模块
_Ht=require("child_process")

// 字节偏移 1424484: SDK 定位 cli.js 路径
let pe = fileURLToPath(require("url").pathToFileURL(__filename).href);
let be = join(pe, "..");
l = join(be, "cli.js");

// 字节偏移 1560485: ESe 类的 spawnLocalProcess 方法
_Ht.spawn(t, r, {cwd:a, stdio:["pipe","pipe",c], signal:s, env:o, windowsHide:!0})

// 字节偏移 1563926: initialize() 中验证 cli.js 存在并 spawn
if (!vB().existsSync(c)) throw new ReferenceError("Claude Code executable not found at " + c);
this.process = this.spawnLocalProcess(de);
```

**3. 日志也证实了子进程存在**：
```
claude-agent-sdk stderr: Error in hook callback hook_6: VJ [AbortError]
```
`stderr:` 标签说明 extension.js 在捕获子进程的 stderr 输出。

### 结论

**官方说法部分正确但不完整**：
- ✅ SDK 确实是通过 `import()` 在 extension.js 进程内加载的
- ✅ 确实有 localhost HTTP 代理 (`ClaudeLanguageModelServer`)
- ✅ `selectEndpoint()` 确实做模型映射
- ❌ **但 SDK 内部 spawn 了 cli.js 作为独立子进程**，这是官方没提到的
- SDK 代码在 extension.js 进程中运行，但 **实际的 Claude 命令行工具（cli.js）是被 SDK spawn 出来的子进程**
- cli.js 子进程通过 `ANTHROPIC_BASE_URL=http://localhost:{port}` 将请求发回代理服务器

### 对第三方中继的影响

因为有 localhost 代理这一层，理论上只需要在代理层 (`selectEndpoint()`) 做模型映射就够了。
**但实际上 cli.js 补丁仍然需要**，原因：

1. **`selectEndpoint()` 的映射逻辑有限** — 它只做简单的 `split('-')` 转换和 `includes()` 匹配，无法处理第三方中继站需要的完整日期后缀格式
2. **代理层实际转发目标** — 代理通过 VS Code Language Model API 转发，这个 API 的 endpoint 配置取决于 extension.js 中的 `__mapModel` 补丁
3. **cli.js 构建请求时的模型 ID** 影响代理如何选择 endpoint — 如果 cli.js 发送 `claude-opus-4-6-20260205`，代理的 `selectEndpoint()` 能更精确地匹配
4. **双重保障** — 在 cli.js 和 extension.js 两层都做映射，确保无论哪层处理，模型 ID 都是正确的

## 关键教训：cli.js 才是真正的 API 调用者

### 问题现象

只补丁 extension.js 后：
- VS Code Output 面板显示的 `ccreq:` 日志中看不到模型映射效果
- Claude Agent 发送请求时使用未映射的模型 ID（如 `claude-opus-4.6`）
- 第三方中继站收到无法识别的模型名，导致：
  - 请求超时（30+ 秒）
  - 非流式响应（中继站对未知模型可能不开启流式）
  - `AbortError` 错误
  - UI 完全卡死

### 发现过程

1. 在错误堆栈中发现所有 API 调用都来自 `cli.js`，不是 `extension.js`
2. 在 extension.js 中搜索 `__mapModel` 有 3 处引用，但 cli.js 中 0 处
3. curl 直接调用中继站 API 完全正常（流式、快速响应），证明问题在 VS Code 端
4. 确认 extension.js 只处理 UI，cli.js 才发出 HTTP 请求
5. 进一步分析发现 cli.js 的 HTTP 请求实际发往 localhost 代理，代理再转发到真实 API

### 结论

**必须同时补丁 extension.js 和 cli.js。** extension.js 补丁确保代理层的模型映射和 UI 面板显示正确的模型名，cli.js 补丁确保发往代理的请求就已经使用正确的模型 ID（双重保障）。

## cli.js 补丁的坑

### 坑 1: `"use strict"` 不在文件顶层

```javascript
// ❌ 错误认知：以为 "use strict" 在文件顶层
"use strict"; var __cliModelMap = {...}  // 实际被注入到 Function() 构造器内部

// 实际结构：
X6Q = Function;
IO1 = function(A) {
    try {
        return X6Q('"use strict"; return ('+A+").con..." )
        //           ↑ "use strict" 在这里！在动态创建的函数字符串内部
    }
}
```

如果用 `content.replace('"use strict";', '"use strict";' + injectionCode)` 注入，代码会被插入到 `Function()` 构造器的字符串参数中，而不是文件顶层。导致 `var __cliMap` 定义在一个动态函数作用域内，其他函数完全看不到。

**报错信息**: `API Error: cliMap is not defined`

### 坑 2: cli.js 是 ES Module

cli.js 文件头部是：
```javascript
#!/usr/bin/env node
// comments...
import{createRequire as lN9}from"node:module";
```

使用 `import` 语法，说明是 ES Module。在 ES Module 中：
- `var` 声明的变量是模块级作用域，不是全局的
- 不同模块/作用域之间无法通过 `var` 共享变量
- 必须使用 `globalThis` 挂载到全局对象

### 坑 3: sed 对 11MB 文件不可靠

使用 `sed -i` 对 11MB 的单行文件做复杂替换会出问题：
- 正则表达式匹配可能不精确
- 特殊字符转义困难
- 可能产生重复替换

**推荐**：使用 Python 脚本进行精确的字符串替换。

### 正确的补丁方式

```javascript
// 1. 注入位置：import 语句之前（文件真正的顶层）
globalThis.__cliModelMap = {
    "claude-opus-4.6": "claude-opus-4-6-20260205",
    // ...
};
globalThis.__cliMap = function(m) {
    return globalThis.__cliModelMap[m] || m;
};
import{createRequire as lN9}from"node:module"; // 原始代码

// 2. 使用 globalThis 确保全局可访问
// 3. 在 3 个关键函数入口处调用 globalThis.__cliMap()
```

## cli.js 函数签名发现方法

cli.js 是 minified 的（11MB 单行），函数名是混淆后的（如 `g81`, `Gu`, `nH`）。每次 Copilot Chat 插件更新，这些名字都可能变。以下是发现关键函数的方法：

### 1. 流式对话生成器

```bash
# 搜索 async generator 模式，找到有 model:B.model 的那个
grep -oP 'async function\*\w+\([^)]+\)\{[^}]{0,200}' cli.js | grep 'model:B.model'
```

这个函数是主要的对话流式生成器，类似 `async function*g81(A,Q,B){...}`。

### 2. ANSI 清理函数

```bash
# 搜索 ANSI 清理模式
grep -oP 'function \w+\(A\)\{return A\.replace\(/\\\[.+?/gi,""\)\}' cli.js
```

这个函数用于清理模型名中的 ANSI 转义序列，输出被用在 `model:Gu(X)` 形式的调用中。

### 3. Anthropic 客户端工厂

```bash
# 搜索包含 apiKey, maxRetries, model, fetchOverride 参数解构的 async 函数
grep -oP 'async function \w+\(\{apiKey:\w+,maxRetries:\w+,model:\w+,fetchOverride:\w+\}\)' cli.js
```

### 4. 快速验证映射

```bash
# 搜索所有 model: 相关的上下文
grep -oP 'model:\w+\(\w+\).{0,30}' cli.js | head -20

# 搜索 stream:!0 (stream=true) 和 stream:!1 (stream=false) 统计
grep -c 'stream:!0' cli.js  # 流式调用数
grep -c 'stream:!1' cli.js  # 非流式调用数
```

### 5. 验证补丁效果

```bash
# 补丁后，应该能看到 globalThis.__cliMap 引用
grep -oP 'globalThis\.__cliMap.{0,60}' cli.js

# 应该有 4 个引用：
# 1. 定义：globalThis.__cliMap=function(m){...}
# 2. Gu 函数内：globalThis.__cliMap(A.replace(...))
# 3. nH 函数内：globalThis.__cliMap(B||"")
# 4. 流式生成器内：globalThis.__cliMap(B.model)
```

## 模型 ID 映射机制

### VS Code 内部 ID vs 中继站 ID

VS Code Copilot Chat 插件使用的模型 ID 是简化格式：

| VS Code 内部 ID | 含义 | 示例中继站 ID |
|---|---|---|
| `claude-opus-4.6` | 最强模型 | `claude-opus-4-6-20260205` |
| `claude-sonnet-4.5` | 平衡模型 | `claude-sonnet-4-5-20250929` |
| `claude-haiku-4.5` | 快速模型 | `claude-haiku-4-5-20251001` |

中继站（如 New API / One API）通常需要完整的日期后缀才能正确路由到对应的上游 API 渠道。

### settings.json 环境变量

`~/.claude/settings.json` 中的关键环境变量：

| 变量 | 作用 | 示例值 |
|---|---|---|
| `ANTHROPIC_BASE_URL` | API 基础 URL | `https://ai.example.com` |
| `ANTHROPIC_API_KEY` | API 密钥 | `sk-...` |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Opus 默认模型 | `claude-opus-4-6-20260205` |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Sonnet 默认模型 | `claude-sonnet-4-5-20250929` |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Haiku 默认模型 | `claude-haiku-4-5-20251001` |
| `ANTHROPIC_SMALL_FAST_MODEL` | 快速任务模型 | `claude-haiku-4-5-20251001` |
| `API_TIMEOUT_MS` | 超时（毫秒） | `3000000` |

**注意**：`ANTHROPIC_DEFAULT_SONNET_MODEL` 如果设置为 Opus 模型 ID，会导致所有 Sonnet 请求被路由到 Opus，严重影响响应速度（Sonnet 用于轻量规划任务，应快速响应）。

## 常见问题排查

### UI 卡死 / AbortError

**可能原因**：
1. cli.js 未打补丁 → 发送了未映射的模型 ID → 中继站超时
2. `ANTHROPIC_DEFAULT_SONNET_MODEL` 设成了 Opus → Sonnet 请求走 Opus 通道 → 响应慢
3. 中继站 504 → Cloudflare 超时 → 后端过载

**排查方法**：
```bash
# 1. 检查 cli.js 是否已补丁
grep -c 'globalThis.__cliMap' ~/.vscode/extensions/github.copilot-chat-*/dist/cli.js

# 2. 检查中继站连通性
curl -s -o /dev/null -w "%{http_code}" https://your-relay.com/v1/models -H "Authorization: Bearer sk-xxx"

# 3. 测试流式响应
curl -N -X POST "https://your-relay.com/v1/messages" \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-opus-4-6-20260205","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

### hook_6 AbortError（可忽略）

日志中出现类似：
```
claude-agent-sdk stderr: Error in hook callback hook_6: VJ [AbortError]
```

这是 **正常现象**，不影响功能。它是 `gpt-4o-mini` 内部请求（GitHub Copilot 用于意图分析/上下文总结）的某个钩子回调被取消。通常紧跟着就会有 `success` 的请求结果。这个错误走的是 GitHub 官方通道，与中继站无关。

### gpt-4o-mini 超时 30 秒

这是 **GitHub 自己的后端问题**，不是中继站的问题。`gpt-4o-mini` 是 Copilot 插件内部用于上下文总结的模型，走的是 GitHub 官方通道。GitHub 后端偶尔会返回 502 "Unicorn" 页面。

**特征**：日志中 `gpt-4o-mini | 30735ms | failed` + 502 HTML 包含 "GitHub Unicorn"。

### 中继站显示"非流"

部分中继站（如 New API / One API）对非标准模型 ID 可能默认不开启流式。确保映射后的模型 ID 是中继站已配置且支持流式的。

## 版本升级注意事项

当 `github.copilot-chat` 扩展更新时：

1. **extension.js 和 cli.js 会被覆盖**，补丁丢失
2. **备份文件不受影响**（`.claude-relay-backup` 后缀）
3. 需要重新运行 claude-relay 部署
4. 如果 cli.js 的 minified 函数名变了，`cli_patcher.go` 中的模式匹配可能失败
5. 失败时会返回错误提示，需要重新发现函数签名（见上文方法）

### 自动检测建议流程

```bash
# 检查扩展版本
ls ~/.vscode/extensions/ | grep copilot-chat

# 检查是否有现有补丁
grep -c 'claude-relay-patch-begin' ~/.vscode/extensions/github.copilot-chat-*/dist/extension.js
grep -c 'globalThis.__cliMap' ~/.vscode/extensions/github.copilot-chat-*/dist/cli.js

# 如果都是 0，说明需要重新部署
```

---

> **写给 AI 助手的备注**：如果用户报告 Claude Agent 卡死或模型 ID 不对，第一件事是检查 cli.js 是否有 `globalThis.__cliMap`。如果没有，那就是根本原因。不要只看 extension.js。注意：cli.js 的 HTTP 请求实际上发往 extension.js 内部的 localhost 代理，代理再转发到真实 API。所以 extension.js 的 `selectEndpoint()` 映射和 `__mapModel` 补丁同样重要。两个文件都必须补丁。`hook_6 AbortError` 和 `gpt-4o-mini` 超时是 GitHub 后端问题，与中继站无关，可以忽略。
