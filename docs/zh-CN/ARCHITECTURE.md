# Vellum — 架构

本文说明 Vellum 的构建方式。功能与用法见 [README.zh-CN.md](../../README.zh-CN.md)。

## 设计目标

1. **超轻量** —— 单一 Go 静态二进制；仅一个外部模块（`golang.org/x/net/html`）；
   无运行时服务，无需 GPU。
2. **超精确** —— 输出确定性（排序后的链接、预先分配的合法文件名、SHA-256
   校验）、严格的 PDF 校验、原子写入。
3. **资源受限** —— 默认并发为逻辑 CPU 的 60%，所有响应体都有大小上限，不会
   挤占宿主机器资源。
4. **安全** —— URL 校验、文件名清洗、路径包含防护、所有 I/O 均有超时、代码中
   不含密钥。

## 包结构

```
cmd/vellum/        CLI 入口，只做参数解析，不含业务逻辑。
internal/config/   默认值、VELLUM_* 环境变量覆盖、校验。
internal/fetch/    礼貌、带重试的 HTTP 客户端（UA、退避、大小上限）。
internal/crawl/    同站 BFS 发现 PDF 链接。
internal/download/ 受限工作池：流式下载、原子写入、sha256、断点续传。
internal/manifest/ JSON 清单（原子写入）。
internal/app/      组合根，端到端串联整个流程。
```

依赖方向严格且无环：

```
app ──► config, fetch, crawl, download, manifest
```

下层包绝不反向导入上层包；每个包都自行声明其协作方所需的最小接口
（`crawl.Fetcher`、`download.Fetcher`），从而可用 stub 做单元测试。

## 关键决策

### 60% CPU 预算

`config.AutoConcurrency` 计算 `int(0.6 * NumCPU)`，下限为 1。下载器使用恰好
这么多个 worker。在参考机器（Ryzen 7 3700U，8 逻辑核）上即 4 个 worker。
内存通过单文件大小上限与流式拷贝（绝不整份缓冲 PDF）来约束。

### 重试与礼貌

`fetch.Client` 强制执行请求间最小间隔（默认 250 ms），并对瞬时失败 —— 网络
错误、408/429、5xx —— 做指数退避重试。取消或 4xx（除 408/429 外）不重试。
每个请求都携带真实 User-Agent 与单次请求超时。

### 确定性命名

`download.assignNames` 在任何 worker 启动前就把每个 URL 映射到文件名，因此
结果与 goroutine 调度无关、可复现。冲突时追加短 SHA-256 后缀。文件名由
`sanitizeName` 从 URL 基名清洗：替换非法字符、保证 `.pdf` 后缀，并由
`safeJoin` 拒绝任何可能逃逸输出目录的名称。

### 尽力而为的发现

`crawl.Crawler` 按深度上限对同站 HTML 页面做广度优先遍历。非起始页失败会被
跳过（继续抓取）；只有起始页失败才是致命的。站外 PDF 链接（如学术站点）会被
记录，但不会继续抓取进去。

### 数据契约

`manifest.json` 是交接给 RAG 阶段的机器可读产物。对每份已发现 PDF 记录：源
URL、最终（重定向后）URL、本地路径、大小、SHA-256、Content-Type、状态
（`downloaded` / `skipped` / `failed`）、发现页面、锚文本标题与抓取时间。

## 路线图

1. **Scraper**（已交付）—— `vellum scrape` → `data/manifest.json`。
2. **Ingest** —— 从 PDF 抽取文本、分块、构建混合索引（BM25 词法检索 + 轻量
   稠密嵌入；仍为 CPU-only）。
3. **Query** —— `vellum query "..."` 返回带引用的排序片段。
4. **Skill** —— 把 query CLI 封装为 Reasonix skill，让宿主 agent 基于检索到的
   上下文作答，无需额外的 LLM API key 或本地模型服务。
