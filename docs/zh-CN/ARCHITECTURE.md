# Vellum — 架构

本文说明 Vellum 的构建方式。功能与用法见 [README.zh-CN.md](../../README.zh-CN.md)。

## 设计目标

1. **超轻量** —— 单一 Go 静态二进制；仅两个小型模块（`golang.org/x/net/html`、
   `github.com/ledongthuc/pdf`）；无运行时服务，无需 GPU，无向量化模型服务。
2. **超精确** —— 输出确定性（排序后的链接、预先分配的合法文件名、SHA-256
   校验）、严格的 PDF 校验、原子写入，且检索会为每个片段返回源 PDF。
3. **资源受限** —— 默认并发为逻辑 CPU 的 60%，所有响应体都有大小上限。
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
internal/text/     PDF → 文本抽取（pdftotext + 纯 Go 回退）+ 分词器。
internal/chunk/    按段落/句子分块，附带来源元数据。
internal/index/    BM25 索引：构建、检索、JSON 持久化。
internal/ingest/   编排 manifest → 文本 → 分块 → 索引 → 保存。
internal/query/    加载索引并渲染排名 + 引用结果。
internal/app/      scrape 流水线的组合根。
```

依赖方向严格且无环；下层包绝不反向导入上层包，每个包都自行声明其协作方所需的
最小接口（`crawl.Fetcher`、`download.Fetcher`、`text.Extractor`）以便测试。

## 关键决策

### 60% CPU 预算

`config.AutoConcurrency` 计算 `int(0.6 * NumCPU)`，下限为 1。下载器使用恰好
这么多个 worker。内存通过单文件大小上限与流式拷贝来约束。

### 重试与礼貌

`fetch.Client` 强制执行请求间最小间隔，并对瞬时失败（网络错误、408/429、5xx）
做指数退避重试。取消或非瞬时的 4xx 不重试。

### 确定性命名

`download.assignNames` 在任何 worker 启动前就把每个 URL 映射到文件名，因此
结果与 goroutine 调度无关、可复现。冲突时追加短 SHA-256 后缀；`sanitizeName`
替换非法字符，`safeJoin` 拒绝任何可能逃逸输出目录的名称。

### 文本抽取（可插拔）

`text.Default()` 在存在 poppler `pdftotext` 时优先使用（对学术 PDF 保真度最高），
否则回退到纯 Go 解析器。选择抽象为 `Extractor` 接口，日后可换入 OCR 或其他后端。
无文本层的 PDF（扫描件）会被跳过并记录为 `no_text`。

### 检索（BM25）

`index.Build` 对每个片段分词并构建倒排索引（词项 postings + 文档长度）。
`index.Search` 用 BM25（k1=1.5，b=0.75）对片段打分，且文档与查询使用同一分词器，
保证分词一致。对约 20 篇论文的语料，整个索引常驻内存，查询为微秒级。分词器保留
交易术语，丢弃一个小型英文停用词表。

### 数据契约

- `data/manifest.json` —— 每份 PDF 的下载结果（URL、最终 URL、本地路径、大小、
  SHA-256、Content-Type、状态、发现页面、标题、抓取时间）。
- `data/index.json` —— BM25 索引：所有片段（含来源元数据）加 postings 与文档
  长度，原子写入。

## 路线图

1. ✅ **Scrape** —— `vellum scrape` → `data/manifest.json`。
2. ✅ **Ingest** —— `vellum ingest` → `data/index.json`（文本 → 分块 → BM25）。
3. ✅ **Query** —— `vellum query "..."` → 排名 + 引用片段。
4. ✅ **Skill** —— `/vellum-rag` 封装 `vellum query`；宿主 agent 依据检索证据
   作答，无需额外 LLM / 向量化 API key。
5. **未来** —— 在 `Extractor` / 索引接口之后加入稠密（语义）嵌入、扫描件 OCR，
   以及 `vellum serve` HTTP 接口。
