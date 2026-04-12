# taro 技术设计文档
## 1. 技术选型

### 1.1 主服务（taro）

| 组件 | 选型 | 理由 |
|------|------|------|
| 语言 | Go 1.22+ | 内存占用小，适合树莓派；静态编译单二进制部署 |
| Web 框架 | 标准库 `net/http` | Go 1.22 路由已足够，无额外依赖，内存最小 |
| 模板引擎 | `github.com/a-h/templ` | 类型安全的 Go 模板，编译期检查，配合 HTMX |
| 前端交互 | HTMX（CDN 引入） | 无需 Node 构建，服务端渲染，轻量现代 |
| 数据库 | SQLite via `modernc.org/sqlite` | 纯 Go 实现，无 CGO，树莓派交叉编译友好 |
| SQL 工具 | `github.com/jmoiron/sqlx` | 轻量，比 ORM 更直接，比裸 `database/sql` 更方便 |
| PikPak CLI | `github.com/Bengerthelorf/pikpaktui` | 活跃维护的 Rust CLI（2026年持续更新），通过 `exec.Command` 调用，支持离线下载、文件管理、删除；认证通过 `PIKPAK_USER`/`PIKPAK_PASS` 环境变量传入 |
| TG Bot | `github.com/go-telegram-bot-api/telegram-bot-api` | 最成熟稳定，stars 最多，inline keyboard 支持完整 |
| 配置解析 | `github.com/spf13/viper` | 支持 YAML + 环境变量覆盖，自动绑定 |
| 定时任务 | `github.com/robfig/cron/v3` | 轻量 cron 调度，支持秒级精度 |
| HTTP 客户端 | 标准库 `net/http` | 够用，不引入额外依赖 |

### 1.2 转存服务（taro-transfer）

| 组件 | 选型 | 理由 |
|------|------|------|
| 语言 | Go | 与主服务一致，单二进制部署 |
| Web 框架 | 标准库 `net/http` | 接口极简，3 个端点 |
| rclone | 系统调用 `rclone` 二进制 | 直接 exec，不引入 rclone 库依赖 |
| 任务存储 | 内存 Map（sync.Map） | 无需持久化，HF Space 重启后主服务会重新提交 |

### 1.3 外部 API 集成

| 服务 | 认证方式 | 关键端点 |
|------|----------|----------|
| Bangumi | OAuth2 Bearer Token | `GET /v0/users/{uid}/collections?subject_type=2&type=1`（想看） |
| Trakt | OAuth2 Device Code Flow | `GET /sync/watchlist`、`POST /sync/collection` |
| Prowlarr | API Key（Header `X-Api-Key`） | `GET /api/v1/search?query=&type=search&indexerIds=` |
| PikPak | 账号密码（通过 `pikpaktui` CLI 传入） | 通过 `pikpaktui` CLI 调用，支持离线下载、文件删除 |
| Jellyfin | Webhook 插件推送 | `POST /webhook/jellyfin`（被动接收） |

---

## 2. 系统架构

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────┐
│                    树莓派 Docker                          │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │                  taro 主服务                      │   │
│  │                                                 │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │   │
│  │  │  Poller  │  │ Searcher │  │  Downloader  │  │   │
│  │  │(定时轮询) │  │(Prowlarr)│  │  (PikPak)    │  │   │
│  │  └────┬─────┘  └────┬─────┘  └──────┬───────┘  │   │
│  │       │             │               │           │   │
│  │  ┌────▼─────────────▼───────────────▼───────┐  │   │
│  │  │              State Machine               │  │   │
│  │  │              (SQLite DB)                 │  │   │
│  │  └────┬─────────────┬───────────────┬───────┘  │   │
│  │       │             │               │           │   │
│  │  ┌────▼──────┐ ┌────▼────┐ ┌────────▼───────┐  │   │
│  │  │ Transfer  │ │Webhook  │ │    Notifier    │  │   │
│  │  │Coordinator│ │ Handler │ │  (Telegram)    │  │   │
│  │  └────┬──────┘ └─────────┘ └────────────────┘  │   │
│  │       │                                         │   │
│  │  ┌────▼──────┐ ┌─────────┐ ┌────────────────┐  │   │
│  │  │  WebUI    │ │   CLI   │ │    TG Bot      │  │   │
│  │  │(templ+    │ │(cobra)  │ │(bot-api)       │  │   │
│  │  │ HTMX)     │ │         │ │                │  │   │
│  │  └───────────┘ └─────────┘ └────────────────┘  │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  ┌──────────────┐    ┌──────────────────────────────┐  │
│  │   Jellyfin   │    │  OneDrive (rclone 挂载)       │  │
│  │  (webhook)   │    │  /mnt/onedrive/media/         │  │
│  └──────────────┘    └──────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
         │ POST /transfer          ▲ GET /transfer/{id}/status
         ▼                         │
┌─────────────────────────────────────────────────────────┐
│              HuggingFace Space Docker                    │
│                                                         │
│              taro-transfer 服务                          │
│         (rclone: PikPak → OneDrive)                     │
└─────────────────────────────────────────────────────────┘
```

### 2.2 项目目录结构

```
taro/
├── cmd/
│   ├── taro/
│   │   └── main.go          # 主服务入口（server + bot + scheduler）
│   └── taroctl/
│       └── main.go          # CLI 工具入口
├── internal/
│   ├── config/
│   │   └── config.go        # 配置加载（viper）
│   ├── db/
│   │   ├── db.go            # 数据库连接与迁移
│   │   ├── entry.go         # Entry CRUD
│   │   └── schema.sql       # 建表 SQL
│   ├── state/
│   │   └── machine.go       # 状态机核心逻辑
│   ├── poller/
│   │   ├── bangumi.go       # Bangumi 轮询
│   │   └── trakt.go         # Trakt 轮询
│   ├── searcher/
│   │   └── prowlarr.go      # Prowlarr 搜索
│   ├── downloader/
│   │   └── pikpak.go        # PikPak 下载管理
│   ├── transfer/
│   │   └── coordinator.go   # 转存协调（轮询 taro-transfer）
│   ├── webhook/
│   │   └── jellyfin.go      # Jellyfin webhook 处理
│   ├── platform/
│   │   ├── bangumi.go       # Bangumi 状态回调
│   │   └── trakt.go         # Trakt 状态回调
│   ├── notifier/
│   │   └── telegram.go      # Telegram 通知
│   ├── bot/
│   │   └── bot.go           # TG Bot 交互逻辑
│   ├── web/
│   │   ├── server.go        # HTTP 路由注册
│   │   ├── handlers/        # 各页面 handler
│   │   └── templates/       # templ 模板文件
│   ├── scheduler/
│   │   └── scheduler.go     # cron 任务注册
│   └── health/
│       └── onedrive.go      # OneDrive 挂载健康检测
├── taro-transfer/
│   ├── main.go              # taro-transfer 服务入口
│   ├── handler.go           # HTTP 接口处理
│   ├── task.go              # 任务状态管理
│   └── Dockerfile           # taro-transfer 镜像
├── config.yaml.example      # 配置文件示例
├── Dockerfile               # taro 主服务镜像
└── docker-compose.yml       # 树莓派部署编排
```

---
## 3. 数据库设计

### 3.1 表结构

```sql
-- 媒体条目主表
CREATE TABLE entries (
    id              TEXT PRIMARY KEY,          -- UUID
    title           TEXT NOT NULL,             -- 媒体标题
    year            INTEGER,                   -- 年份（用于电影搜索）
    media_type      TEXT NOT NULL,             -- 'anime' | 'movie' | 'tv'
    source          TEXT NOT NULL,             -- 'bangumi' | 'trakt' | 'manual'
    source_id       TEXT NOT NULL,             -- 平台原始 ID（Bangumi subject_id / Trakt slug / manual 时为 UUID）
    season          INTEGER NOT NULL DEFAULT 1,-- 季数（电影为 0，剧集/动漫从 1 开始；创建电影条目时需显式传 0）
    status          TEXT NOT NULL DEFAULT 'pending',
    ask_mode        INTEGER NOT NULL DEFAULT 0,-- 0=全局配置 1=强制询问 2=强制自动
    resolution      TEXT,                      -- 条目级分辨率覆盖（NULL=使用全局）
    -- 搜索结果
    selected_resource_id TEXT,                 -- 选中的资源 ID（关联 resources 表）
    -- 阶段开始时间（用于超时判断和恢复逻辑）
    search_started_at    DATETIME,             -- 搜索开始时间
    download_started_at  DATETIME,             -- 下载开始时间
    transfer_started_at  DATETIME,             -- 转存开始时间
    -- PikPak 信息
    pikpak_task_id  TEXT,                      -- PikPak 离线下载任务 ID
    pikpak_file_id  TEXT,                      -- PikPak 文件 ID
    pikpak_file_path TEXT,                     -- PikPak 内部文件路径（裸路径，不含 pikpak: 前缀，transfer 服务负责拼接）
    pikpak_cleaned  INTEGER NOT NULL DEFAULT 0,-- PikPak 文件是否已清理
    -- 转存信息
    transfer_task_id TEXT,                     -- taro-transfer 任务 ID
    target_path     TEXT,                      -- OneDrive 目标路径
    -- 失败信息（结构化）
    failed_stage    TEXT,                      -- 失败时所处阶段（'searching'|'downloading'|'transferring'）
    failed_reason   TEXT,                      -- 失败原因描述（人类可读）
    failure_kind    TEXT,                      -- 'retryable' | 'permanent'（失败分类）
    failure_code    TEXT,                      -- 结构化失败代码（见 FailureCode 枚举）
    failed_at       DATETIME,
    -- 时间戳
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source, source_id, season)          -- 防止重复条目
);

-- 搜索结果资源表（存储所有搜索结果，含被过滤项，条目进入终态后可清理）
-- 设计原则：存全部搜索结果，用 eligible 字段区分可选/被过滤
-- 理由：用户需要知道"为什么这个 AV1 被过滤了"，只写日志不够持久
CREATE TABLE resources (
    id          TEXT PRIMARY KEY,              -- UUID
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    title       TEXT NOT NULL,                 -- 资源文件名
    magnet      TEXT NOT NULL,                 -- 磁力链接
    size        INTEGER,                       -- 文件大小（字节）
    seeders     INTEGER,                       -- 做种数
    resolution  TEXT,                          -- 解析出的分辨率（'1080p'|'1080i'|'720p'|'480p'|'other'）
    codec       TEXT,                          -- 解析出的编码（'x264'|'x265'|'av1'|'unknown'）
    indexer     TEXT,                          -- 来源索引器名称
    eligible    INTEGER NOT NULL DEFAULT 1,    -- 是否可选（0=被过滤，不参与自动选择和 UI 展示）
    score       INTEGER,                       -- 综合评分快照（仅 eligible=1 时有意义）
    selected    INTEGER NOT NULL DEFAULT 0,    -- 是否被选中（最终选中的资源）
    rejected_reason TEXT,                      -- 被过滤的原因（仅 eligible=0 时有意义，如 "codec_excluded:av1"）
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 状态变更审计日志表
CREATE TABLE state_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT,                          -- 变更原因（可选）
    metadata    TEXT,                          -- JSON 格式元信息（v2 候选：记录 resource_id、token_refreshed 等）
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 索引
CREATE INDEX idx_entries_status ON entries(status);
CREATE INDEX idx_entries_source ON entries(source, source_id);
CREATE INDEX idx_entries_failure_kind ON entries(failure_kind) WHERE status = 'failed';
CREATE INDEX idx_resources_entry ON resources(entry_id);
CREATE INDEX idx_resources_eligible ON resources(entry_id, eligible); -- 快速查询可选资源
CREATE INDEX idx_state_logs_entry ON state_logs(entry_id);
CREATE INDEX idx_entries_failed_at ON entries(failed_at) WHERE status = 'failed';
```

### 3.2 状态枚举与失败分类

```go
type EntryStatus string

const (
    StatusPending       EntryStatus = "pending"
    StatusSearching     EntryStatus = "searching"
    StatusFound         EntryStatus = "found"
    StatusDownloading   EntryStatus = "downloading"
    StatusDownloaded    EntryStatus = "downloaded"
    StatusTransferring  EntryStatus = "transferring"
    StatusTransferred   EntryStatus = "transferred"
    StatusInLibrary     EntryStatus = "in_library"
    StatusNeedsSelection EntryStatus = "needs_selection"
    StatusFailed        EntryStatus = "failed"
    StatusCancelled     EntryStatus = "cancelled"
)

// 失败分类
type FailureKind string

const (
    FailureRetryable  FailureKind = "retryable"  // 可重试：网络超时、服务不可达、临时认证失败
    FailurePermanent  FailureKind = "permanent"  // 不可重试：无资源、资源损坏、用户取消、配置错误
)

// 失败代码（结构化失败原因，持久化到 entries.failure_code）
type FailureCode string

const (
    // Retryable failures（可重试）
    FailureNetworkTimeout     FailureCode = "network_timeout"
    FailureServiceUnreachable FailureCode = "service_unreachable"
    FailureAuthTemporary      FailureCode = "auth_temporary"
    FailurePikPakTimeout      FailureCode = "pikpak_timeout"
    FailureTransferTimeout    FailureCode = "transfer_timeout"

    // Permanent failures（不可重试）
    FailureNoResources        FailureCode = "no_resources"
    FailureAllCodecsExcluded  FailureCode = "all_codecs_excluded"
    FailureUserCancelled      FailureCode = "user_cancelled"
    FailureConfigError        FailureCode = "config_error"
)

// FailureKindOf 返回 FailureCode 对应的 FailureKind
func FailureKindOf(code FailureCode) FailureKind {
    switch code {
    case FailureNoResources, FailureAllCodecsExcluded, FailureUserCancelled, FailureConfigError:
        return FailurePermanent
    default:
        return FailureRetryable
    }
}

// 合法状态转换表
var validTransitions = map[EntryStatus][]EntryStatus{
    StatusPending:        {StatusSearching},
    StatusSearching:      {StatusFound, StatusNeedsSelection, StatusFailed, StatusPending}, // 允许降级回 pending
    StatusFound:          {StatusDownloading},
    StatusDownloading:    {StatusDownloaded, StatusFailed},
    StatusDownloaded:     {StatusTransferring},
    StatusTransferring:   {StatusTransferred, StatusFailed},
    StatusTransferred:    {StatusInLibrary},
    StatusNeedsSelection: {StatusFound, StatusCancelled},
    StatusFailed:         {StatusPending, StatusDownloaded}, // 智能重试可能恢复到 downloaded
    // 任意非终态可转为 cancelled
}
```

---

## 4. 核心模块设计

### 4.0 职责边界规则（强制约束）

系统中所有模块必须遵守以下职责边界，不得越界：

| 层级 | 职责 | 禁止事项 |
|------|------|----------|
| **StateMachine** | 唯一有权修改条目状态的组件；执行状态合法性校验；写入审计日志 | 不执行业务逻辑 |
| **Scheduler（调度器）** | 扫描可执行条目；调用对应业务服务 | 不直接修改状态；不执行业务逻辑 |
| **业务模块**（Searcher/Downloader/Transfer 等） | 执行具体业务操作；通过 StateMachine 请求状态迁移 | 不直接操作数据库状态字段；不绕过 StateMachine |
| **ActionService（动作服务层）** | 统一封装用户触发的动作（retry/cancel/select/add）；供 WebUI/Bot/CLI 共享调用 | 不实现业务逻辑；只编排调用 |
| **WebUI / Bot / CLI** | 接收用户输入；调用 ActionService | 不直接调用 StateMachine；不直接修改数据库 |

这条规则确保：无论从哪个交互入口触发，状态变更行为完全一致。

### 4.1 状态机模块（state/machine.go）

状态机是整个系统的核心，所有状态变更必须通过它进行。

```go
type StateMachine struct {
    database *db.DB
    logger   *slog.Logger
    mu       sync.Mutex
}

// Transition 执行状态转换，写入审计日志
func (sm *StateMachine) Transition(ctx context.Context, entryID string, to EntryStatus, reason string) error

// TransitionWithUpdate 状态转换同时更新其他字段
// 状态机自动设置阶段开始时间：
//   searching  -> 自动设置 search_started_at
//   downloading -> 自动设置 download_started_at
//   transferring -> 自动设置 transfer_started_at
func (sm *StateMachine) TransitionWithUpdate(ctx context.Context, entryID string, to EntryStatus, updates map[string]any) error

// TransitionToFailed 带失败分类的状态转换
// failure_kind 由 FailureKindOf(code) 自动推导，不需要调用方手动指定
func (sm *StateMachine) TransitionToFailed(ctx context.Context, entryID string, code FailureCode, stage, reason string) error

// RecoverOnStartup 系统启动时执行恢复逻辑（接受回调，不直接依赖业务模块）
func (sm *StateMachine) RecoverOnStartup(ctx context.Context, callbacks *RecoveryCallbacks) error
```

**阶段时间字段写入规则（严格定义）：**
- `search_started_at`：仅在状态转换到 `searching` 时由 StateMachine 写入
- `download_started_at`：仅在状态转换到 `downloading` 时由 StateMachine 写入
- `transfer_started_at`：仅在状态转换到 `transferring` 时由 StateMachine 写入
- **禁止预占位**：业务模块不得在状态转换前提前写入阶段时间字段
- 恢复逻辑使用这些字段作为超时基准，而非 `updated_at`
```

**RecoverOnStartup 逻辑：**
1. 将所有 `searching` 状态条目重置为 `pending`
2. 查询所有 `downloading` 状态条目，通过回调将 `(entryID, taskID, download_started_at)` 注册到 Downloader 的轮询队列（使用 `download_started_at` 而非 `updated_at` 作为超时基准）
3. 查询所有 `transferring` 状态条目，通过回调将 `(entryID, taskID, transfer_started_at)` 注册到 TransferCoordinator 的轮询队列
4. `found` 和 `downloaded` 状态的条目由调度器自然恢复（每分钟触发下载/转存）

### 4.2 平台轮询模块（poller/）

两个 Poller 实现同一接口：

```go
type Poller interface {
    Poll(ctx context.Context) error
    Name() string
}
```

**OAuth Token 管理（公共能力）：**

Bangumi 和 Trakt 共享同一套 token 管理逻辑，通过 `OAuthTokenManager` 封装，避免重复实现：

```go
// OAuthTokenManager 管理 OAuth2 token 的刷新与持久化
type OAuthTokenManager struct {
    mu     sync.Mutex
    logger *slog.Logger
}

// EnsureValidToken 确保 token 有效，过期则刷新
// 刷新失败不阻断主流程，写回配置失败只告警不报错
func (m *OAuthTokenManager) EnsureValidToken(ctx context.Context, ...) (string, error)
```

**配置持久化策略：**
- Token 刷新后尝试写回 `config.yaml`
- 写回失败时：记录 WARN 日志，**不阻断主流程**（token 已在内存中更新，当次请求可继续）
- 若配置来自环境变量（`TARO_*`），写回文件可能无意义，系统不强制要求

**BangumiPoller 流程：**
1. 调用 `GET https://api.bgm.tv/v0/users/{uid}/collections?subject_type=2&type=1`（subject_type=2 为动漫，type=1 为想看）
2. 遍历结果，对每个 subject 检查 `entries` 表是否已存在（`source='bangumi' AND source_id=?`）
3. 不存在则创建新条目，`status='pending'`，`media_type='anime'`，标题优先使用 `name`（日文原名）而非 `name_cn`（中文译名），以提高 Nyaa.si 等日文资源站的搜索成功率

**TraktPoller 流程：**
1. 调用 `GET https://api.trakt.tv/sync/watchlist?type=movies,shows`
2. 遍历结果，区分 movie/show，检查是否已存在
3. 对于 show，默认创建第一季条目（season=1）；用户可通过 WebUI/CLI 手动添加其他季

**OAuth2 Token 刷新（两个 Poller 共用逻辑）：**
- 请求前检查 token 是否过期，过期则调用 refresh 端点
- 收到 401 响应时，尝试刷新 token 并重试一次（处理服务端时间偏差等边缘情况）
- 刷新后通过互斥锁保护写回 `config.yaml`（避免并发写入冲突）

### 4.3 资源搜索模块（searcher/prowlarr.go）

```go
type Searcher struct {
    prowlarrURL    string
    apiKey         string
    database       *db.DB
    sm             *state.StateMachine
    client         *http.Client
    logger         *slog.Logger
    excludedCodecs []string
    config         *config.Config
}

func (s *Searcher) Search(ctx context.Context, entry *db.Entry) error
```

**Search 流程：**
1. 将条目状态转为 `searching`（自动记录 `search_started_at`）
2. 构造搜索关键词：
   - Bangumi 动漫：直接使用日文原名（不加 Season 后缀，Bangumi 每季独立条目）
   - Trakt 动漫/剧集：`{title} S{season:02d}`
   - 电影：`{title} {year}`（有年份时）
3. 调用 Prowlarr `GET /api/v1/search`
4. **异常 vs 无结果严格区分（关键设计点）：**
   - Prowlarr 不可达/超时/HTTP 错误 → 条目**保持 pending**，记录日志，下次调度重试（`failure_kind=retryable`）
   - 搜索成功但无结果 → 条目转为 **failed**（`failure_kind=permanent`，`failure_code=no_resources`）
   - 所有结果被编码过滤 → 条目转为 **failed**（`failure_kind=permanent`，`failure_code=all_codecs_excluded`）
5. 解析结果，从标题中提取分辨率和编码，过滤排除的编码
6. 将**所有**搜索结果写入 `resources` 表（含被过滤项）：
   - 可选资源：`eligible=1`，计算 `score`，`rejected_reason=NULL`
   - 被过滤资源：`eligible=0`，`score=NULL`，写入 `rejected_reason`（如 `"codec_excluded:av1"`）
   - 自动选择和 UI 展示只使用 `eligible=1` 的资源
7. 根据询问模式决策：
   - 询问模式关闭 → 按分辨率优先级选最佳 → `found`
   - 询问模式开启 → `needs_selection`，触发 Notifier 发送 TG 消息

**分辨率优先级排序：**
```go
var resolutionPriority = map[string]int{
    "1080p": 4,
    "1080i": 3,
    "720p":  2,
    "480p":  1,
    "other": 0,
}
```

### 4.4 下载管理模块（downloader/pikpak.go）

```go
type PikPakDownloader struct {
    // 通过 pikpaktui CLI 调用，无需 SDK 客户端
    config       *config.Config
    database     *db.DB
    sm           *state.StateMachine
    logger       *slog.Logger
    pollingQueue map[string]*PollingTask // entryID -> task
    queueMu      sync.RWMutex
    stopChan     chan struct{}
    stopOnce     sync.Once
    wg           sync.WaitGroup
}

type PollingTask struct {
    EntryID    string
    TaskID     string
    SubmitTime time.Time // 来自 download_started_at，重启后不重置
}

func (d *PikPakDownloader) Submit(ctx context.Context, entry *db.Entry, magnetURL string) error
func (d *PikPakDownloader) StartPolling(ctx context.Context)
func (d *PikPakDownloader) Stop()
func (d *PikPakDownloader) ResumePolling(ctx context.Context) error
```

**Submit 幂等策略（关键设计点）：**

同一个 entry 在 downloading 阶段只能有一个活跃外部任务，重复提交时优先恢复旧任务：

```
1. 检查 entry.pikpak_task_id 是否已存在
   - 存在 → 查询 PikPak 确认任务是否仍活跃
     - 活跃 → 直接加入轮询队列，不新建任务（使用 download_started_at 作为超时基准）
     - 不活跃 → 继续创建新任务
   - 不存在 → 继续创建新任务

2. 调用 PikPak API 创建远程任务
   - 失败 → 通过 StateMachine.TransitionToFailed 转为 failed（code=service_unreachable）
   - 成功 → 获得 taskID

3. 通过 StateMachine 转为 downloading，同时记录 pikpak_task_id
   （StateMachine 在此步自动写入 download_started_at）

4. 加入轮询队列（使用 download_started_at 作为超时基准）
```

**阶段时间严格语义：**
- `download_started_at` 仅在步骤 3 转换到 `downloading` 时由 StateMachine 写入
- Submit() 不预占位，不提前写时间字段
- 若步骤 3 失败（极少数情况），远程任务已存在但本地未记录 task_id；下次调度时 entry 仍为 `found`，会重新调用 Submit()，步骤 1 会检测到 task_id 为空，重新创建任务（PikPak 侧可能有重复任务，但不影响正确性）

**pikpak_file_path 语义（明确定义）：**
- 存储 PikPak 内部文件路径，**不含** `pikpak:` 前缀
- 示例：`/downloads/进撃の巨人/episode01.mkv`
- transfer 服务使用时拼接为：`rclone copy "pikpak:/downloads/进撃の巨人/episode01.mkv" "onedrive:{target}"`
- 即：transfer 服务负责添加 remote 前缀，字段本身只存路径部分

**轮询逻辑：**
- 间隔从 `config.pikpak.poll_interval` 读取（解析失败时记录 WARN 日志，使用默认 60s）
- 超时判断基于 `PollingTask.SubmitTime`（来自 `download_started_at`，重启后不重置）
- 超时（>24h）→ `failed`（`failure_kind=retryable`，`failure_code=pikpak_timeout`）

### 4.5 转存协调模块（transfer/coordinator.go）

```go
type TransferCoordinator struct {
    transferURL string
    authToken   string
    database    *db.DB
    sm          *state.StateMachine
    logger      *slog.Logger
    pollingQueue map[string]*TransferTask
    queueMu      sync.RWMutex
    stopChan     chan struct{}
    stopOnce     sync.Once
    wg           sync.WaitGroup
}

type TransferTask struct {
    EntryID    string
    TaskID     string
    SubmitTime time.Time // 来自 transfer_started_at，重启后不重置
}
```

**目标路径生成规则：**
```
电影：  /media/movies/{title} ({year})/
剧集：  /media/tv/{title}/Season {season:02d}/
动漫：  /media/anime/{title}/Season {season:02d}/
```

路径规范化规则（避免 Jellyfin 匹配歧义）：
- 统一使用 `/` 作为路径分隔符
- 路径末尾统一带 `/`
- 标题中的特殊字符（`/`、`:`、`*`、`?`、`"`、`<`、`>`、`|`）替换为 `_`

**Submit 幂等策略：**

```
1. 检查 entry.transfer_task_id 是否已存在
   - 存在 → 调用 GET /transfer/{task_id}/status
     - 返回 pending/running/done/failed → 按状态处理，不重新提交
     - 返回 not_found（任务丢失）→ 继续创建新任务
   - 不存在 → 继续创建新任务

2. 调用 POST /transfer 创建任务
   - 失败（服务不可达）→ 条目保持 downloaded，下次调度重试

3. 通过 StateMachine 转为 transferring，同时记录 transfer_task_id
   （StateMachine 在此步自动写入 transfer_started_at）

4. 加入轮询队列（使用 transfer_started_at 作为超时基准）
```

**taro-transfer 补偿行为：**
- 轮询时若 GET /transfer/{task_id}/status 返回 `not_found`，视为 HF Space 已重启，重新提交任务
- 重新提交不改变条目状态（仍为 transferring），只更新 transfer_task_id
- 不设置连续失败计数器（过于复杂），直接依赖 `not_found` 信号判断任务丢失

**Jellyfin 路径匹配规范（统一规则）：**

```go
// normalizePath 规范化路径用于比较
// 规则：统一 / 分隔符、转小写、末尾加 /、去除连续 //
func normalizePath(p string) string

// 匹配逻辑：normalize 后做 prefix match
strings.HasPrefix(normalizePath(webhookPath), normalizePath(entry.TargetPath))
```

禁止直接用原始字符串做前缀匹配，必须先 normalize 两侧路径。

### 4.6 Webhook 处理模块（webhook/jellyfin.go）

```go
// Jellyfin webhook payload（简化）
type JellyfinItemAddedPayload struct {
    NotificationType string `json:"NotificationType"`
    ItemType         string `json:"ItemType"`  // "Movie" | "Episode"
    ItemId           string `json:"ItemId"`
    // Jellyfin webhook 插件支持自定义模板，需配置包含 Path 字段
    Path             string `json:"Path"`
}

func (h *WebhookHandler) HandleJellyfin(w http.ResponseWriter, r *http.Request)
```

**匹配逻辑：**
1. 解析 payload，提取 `Path`
2. 查询数据库中所有 `status='transferred'` 的条目
3. 对 `Path` 和 `target_path` 两侧先做规范化（`normalizePath`），再进行 prefix match：`strings.HasPrefix(normalizePath(Path), normalizePath(target_path))`
4. 匹配成功 → 状态转为 `in_library`，触发平台回调
5. 对于剧集，首次匹配任意一集即更新状态，后续集数的 webhook 因条目已非 `transferred` 状态而自然忽略

**Jellyfin webhook 插件配置说明：**
需在 Jellyfin webhook 插件中配置自定义 JSON 模板，确保包含文件路径字段：
```json
{
  "NotificationType": "{{NotificationType}}",
  "ItemType": "{{ItemType}}",
  "Path": "{{Path}}"
}
```

### 4.7 平台回调模块（platform/）

```go
type PlatformUpdater interface {
    MarkOwned(ctx context.Context, entry *Entry) error
}

// BangumiUpdater：想看 → 在看（type=3）
// POST https://api.bgm.tv/v0/users/-/collections/{subject_id}
// body: {"type": 3}  // 3=在看

// TraktUpdater：加入 collected，移出 watchlist
// POST https://api.trakt.tv/sync/collection
// DELETE https://api.trakt.tv/sync/watchlist/remove
```

### 4.8 通知模块（notifier/telegram.go）

```go
type Notifier struct {
    bot    *tgbotapi.BotAPI
    chatID int64
}

func (n *Notifier) NotifyNewEntry(entry *Entry)
func (n *Notifier) NotifyNeedsSelection(entry *Entry, resources []*Resource)
func (n *Notifier) NotifyInLibrary(entry *Entry)
func (n *Notifier) NotifyFailed(entry *Entry)
func (n *Notifier) NotifyMountDown(mountPath string)
func (n *Notifier) NotifyMountUp(mountPath string)
```

**NotifyNeedsSelection 消息格式：**
```
🎬 需要选择资源：{title} S{season}

候选资源：
[1] {filename} | {size} | {seeders}种 | {resolution}
[2] {filename} | {size} | {seeders}种 | {resolution}
...

[选择 1] [选择 2] [取消]
```
inline keyboard 的 callback_data 格式：`select:{entry_id}:{resource_index}`

### 4.9 动作服务层（internal/service/action.go）

WebUI、TG Bot、CLI 三端共享同一套用户动作逻辑，通过 `ActionService` 统一封装，确保行为一致：

```go
type ActionService struct {
    database   *db.DB
    sm         *state.StateMachine
    downloader *downloader.PikPakDownloader
    logger     *slog.Logger
}

// RetryEntry 重试失败条目（智能重试：根据 failed_stage 决定起点）
// permanent 类型失败直接返回错误，不执行重试
func (s *ActionService) RetryEntry(ctx context.Context, entryID string) error

// CancelEntry 取消条目
func (s *ActionService) CancelEntry(ctx context.Context, entryID string) error

// SelectResource 选择资源（needs_selection -> found）
func (s *ActionService) SelectResource(ctx context.Context, entryID, resourceID string) error

// AddEntry 手动添加条目
func (s *ActionService) AddEntry(ctx context.Context, req AddEntryRequest) (*db.Entry, error)
```

**职责边界明确定义：**

| 动作类型 | 触发方 | 执行路径 |
|---|---|---|
| 用户动作（retry/cancel/select/add） | WebUI / Bot / CLI | → ActionService → StateMachine |
| 系统自动流转（搜索/下载/转存） | Scheduler | → 业务模块（Searcher/Downloader/Transfer）→ StateMachine |

ActionService **只服务于用户入口**，不封装系统自动流转逻辑。系统动作由调度器直接调用各业务模块，业务模块通过 StateMachine 修改状态。不需要再抽 WorkflowService。

**人工介入优先级规则：**
- 用户通过 ActionService 触发的动作具有最高优先级
- 调度器扫描条目时，若条目状态已被用户修改，自然跳过（不会覆盖用户操作）
- `needs_selection` 状态的条目调度器不处理，等待用户选择

### 4.10 TG Bot 模块（bot/bot.go）

```go
type Bot struct {
    bot           *tgbotapi.BotAPI
    actionService *service.ActionService
    notifier      *Notifier
}

func (b *Bot) Start(ctx context.Context)
func (b *Bot) handleCallbackQuery(query *tgbotapi.CallbackQuery)
func (b *Bot) handleCommand(msg *tgbotapi.Message)
```

**支持的命令：**
- `/list` - 列出所有条目
- `/pending` - 列出待选择条目
- `/add <title>` - 手动添加条目
- `/retry <id>` - 重试失败条目（调用 ActionService.RetryEntry）
- `/cancel <id>` - 取消条目（调用 ActionService.CancelEntry）

**Callback 处理：**
解析 `select:{entry_id}:{resource_id}` → 调用 `ActionService.SelectResource`

---

## 5. WebUI 设计

### 5.1 路由结构

```
GET  /                    -> 重定向到 /entries
GET  /entries             -> 条目列表页
GET  /entries/{id}        -> 条目详情页
POST /entries             -> 手动添加条目
POST /entries/{id}/retry  -> 重试失败条目
POST /entries/{id}/cancel -> 取消条目
POST /entries/{id}/select -> 选择资源
GET  /pending             -> 待选择队列页
GET  /status              -> 系统状态页
GET  /health              -> 健康检查端点（Docker healthcheck）
POST /webhook/jellyfin    -> Jellyfin webhook 接收
```

### 5.2 页面设计

条目列表页：按状态分组，HTMX 每 30 秒自动刷新（hx-trigger="every 30s"）。

待选择队列页：展示所有 needs_selection 条目，每个条目展开显示候选资源列表（文件名、大小、做种数、分辨率），点击选择按钮触发 HTMX 局部刷新。

系统状态页：各组件状态、OneDrive 挂载健康状态、最近 50 条状态变更日志。

---

## 6. CLI 设计

使用 github.com/spf13/cobra 构建，CLI 工具名为 `taroctl`，直接读取 SQLite（只读查询），写操作通过 HTTP 调用主服务 WebUI API。

**命令列表：**
```
taroctl list [--status=pending|failed|...]  # 查询 SQLite
taroctl add <title> [--type=anime|movie|tv] [--season=1] [--resolution=1080p] [--ask]  # POST /entries
taroctl pending  # 查询 SQLite
taroctl select <entry_id> <resource_index>  # POST /entries/{id}/select
taroctl cancel <entry_id>  # POST /entries/{id}/cancel
taroctl retry <entry_id>  # POST /entries/{id}/retry
taroctl retry --all  # 批量调用 POST /entries/{id}/retry
taroctl status  # GET /status
```

**部署要求：**
CLI 需与主服务运行在同一台机器或能访问同一 SQLite 文件（Docker 场景需挂载同一 volume）。

---

## 7. 配置文件设计

### 7.1 config.yaml 结构

```yaml
server:
  port: 8080
  db_path: /data/taro.db

logging:
  level: "info"        # debug | info | warn | error
  format: "text"       # text | json

bangumi:
  uid: 123456                # Bangumi 用户数字 ID（通过 GET /v0/me 获取）
  username: "your_username"
  access_token: "xxx"
  refresh_token: "xxx"
  token_expires_at: "2026-01-01T00:00:00Z"
  poll_interval: "24h"

trakt:
  client_id: "xxx"
  client_secret: "xxx"
  access_token: "xxx"
  refresh_token: "xxx"
  token_expires_at: "2026-01-01T00:00:00Z"
  poll_interval: "24h"

prowlarr:
  url: "http://localhost:9696"
  api_key: "xxx"

pikpak:
  username: "your@email.com"
  password: "xxx"
  poll_interval: "5m"
  gc_interval: "24h"
  gc_retention_days: 7

transfer:
  url: "https://your-space.hf.space"
  token: "your_shared_token"
  poll_interval: "2m"

telegram:
  bot_token: "xxx:xxx"
  chat_id: 123456789

onedrive:
  mount_path: "/mnt/onedrive"
  media_root: "/mnt/onedrive/media"
  health_check_interval: "10m"

defaults:
  resolution: "1080p"
  ask_mode: false
  selection_timeout: "24h"
  max_concurrent_searches: 3

retention:
  state_logs_days: 90                  # state_logs 保留天数，0=永久保留
  clean_resources_on_complete: true    # 条目终态后清理候选资源
```

### 7.2 环境变量映射

命名规则：TARO_ + 大写路径，. 替换为 _

```bash
TARO_SERVER_PORT=8080
TARO_BANGUMI_UID=123456
TARO_BANGUMI_ACCESS_TOKEN=xxx
TARO_PIKPAK_USERNAME=xxx
TARO_PIKPAK_PASSWORD=xxx
TARO_TELEGRAM_BOT_TOKEN=xxx
TARO_TELEGRAM_CHAT_ID=123456789
TARO_TRANSFER_URL=https://xxx.hf.space
TARO_TRANSFER_TOKEN=xxx
```

### 7.3 OAuth2 Token 刷新与配置持久化

Token 在过期前 5 分钟自动刷新，刷新后尝试通过 viper.WriteConfig() 持久化回 config.yaml。

**持久化策略：**
- 写回失败时：记录 WARN 日志，**不阻断主流程**（token 已在内存中更新，当次请求可继续）
- 若配置来自环境变量（`TARO_*`），写回文件可能无意义，系统不强制要求写回成功

### 7.4 配置惰性校验（按模块）

启动时不做一刀切的全量必填校验，而是按模块惰性校验：

| 模块 | 必填配置 | 缺失时行为 |
|---|---|---|
| 核心（必须） | `server.port`、`server.db_path`、`prowlarr.url`、`prowlarr.api_key`、`pikpak.username`、`pikpak.password`、`transfer.url`、`transfer.token` | 启动失败 |
| Bangumi Poller | `bangumi.access_token` | 跳过 Bangumi 轮询，记录 WARN |
| Trakt Poller | `trakt.client_id`、`trakt.access_token` | 跳过 Trakt 轮询，记录 WARN |
| Telegram Bot | `telegram.bot_token`、`telegram.chat_id` | 跳过 Bot 和通知，记录 WARN |
| OneDrive Health | `onedrive.mount_path` | 跳过健康检测，记录 WARN |

这样允许用户只配置部分功能（如只跑 WebUI + 手动添加），不会因为 Telegram 未配置而无法启动。

---

## 8. 调度器设计

```go
// 每分钟处理 pending -> 触发搜索（调度器触发，Searcher 执行）
// 每分钟处理 found -> 触发下载
// 每分钟处理 downloaded -> 触发转存
// 每 30 分钟检查 needs_selection 超时
// 每 N 小时 Bangumi/Trakt 轮询
// 每 10 分钟 OneDrive 健康检测
// 每日 PikPak 垃圾回收
```

**并发控制策略：**
- 每个定时任务使用独立的 goroutine，通过 channel 或 context 控制取消
- 同一类型任务（如 Searcher）使用信号量限制并发数（默认最多 3 个并发搜索），避免对 Prowlarr API 造成压力
- 若上一轮任务未完成，下一轮定时到达时跳过本次执行并记录日志

---

## 9. 错误处理策略

### 9.1 失败分类规则

| failure_kind | 含义 | 调度器行为 |
|---|---|---|
| `retryable` | 可重试：网络超时、服务不可达、临时认证失败 | 下次调度自动重试 |
| `permanent` | 不可重试：无资源、编码全被过滤、用户取消 | 不自动重试，需用户手动干预 |

### 9.2 各模块错误处理

| 模块 | 错误类型 | failure_kind | 处理策略 |
|------|----------|---|----------|
| Poller | API 请求失败 | - | 记录日志，跳过本次，下次重试（不影响条目状态） |
| Poller | Token 过期 | - | 自动刷新；写回失败只告警不阻断 |
| Searcher | Prowlarr 不可达/超时 | retryable | 条目**保持 pending**，下次调度重试 |
| Searcher | 无搜索结果 | permanent | 条目转为 failed |
| Searcher | 所有结果被编码过滤 | permanent | 条目转为 failed |
| Downloader | PikPak 提交失败 | retryable | 条目转为 failed，下次可重试 |
| Downloader | 轮询超时（>24h） | retryable | 条目转为 failed，下次可重试 |
| Downloader | PikPak 任务失败 | retryable | 条目转为 failed |
| TransferCoordinator | taro-transfer 不可达 | retryable | 条目**保持 downloaded**，下次重试 |
| TransferCoordinator | 转存失败 | retryable | 条目转为 failed |
| TransferCoordinator | 连续失败超阈值 | - | 重新提交任务（补偿行为） |
| WebhookHandler | 无法匹配条目 | - | 记录日志，返回 200 OK |
| PlatformUpdater | 回调失败 | - | 记录日志，条目保持 in_library |
| Notifier | TG 消息失败 | - | 记录日志，不影响主流程 |

所有 goroutine 使用 `defer recover()` 防止 panic 导致服务崩溃。

---

## 10. 部署设计

### 10.1 树莓派 docker-compose.yml

```yaml
version: '3.8'
services:
  taro:
    image: taro:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - ./data:/data
      - /mnt/onedrive:/mnt/onedrive:ro
    environment:
      - TARO_PIKPAK_PASSWORD=
      - TARO_TELEGRAM_BOT_TOKEN=
      - TARO_TRANSFER_TOKEN=
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health"]
      interval: 30s
      timeout: 5s
      retries: 3
```

### 10.2 taro-transfer Dockerfile（HuggingFace Space）

HuggingFace Space 要求服务监听 7860 端口。rclone.conf 需预先配置好 pikpak 和 onedrive remote。

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o taro-transfer .

FROM alpine:latest
RUN apk add --no-cache rclone
COPY --from=builder /app/taro-transfer /app/taro-transfer
COPY rclone.conf /root/.config/rclone/rclone.conf
EXPOSE 7860
CMD ["/app/taro-transfer"]
```

---

## 11. 正确性属性（Property-Based Testing）

P1 状态转换合法性：任意条目的状态转换序列必须符合 validTransitions 定义，不存在非法跳转。

P2 去重唯一性：对于同一 (source, source_id, season) 组合，数据库中最多存在一条条目记录。

P3 审计日志完整性：条目的 state_logs 记录数量等于其状态转换次数，from_status -> to_status 链路可完整还原条目历史。

P4 Jellyfin 匹配精确性：Webhook_Handler 只匹配 status='transferred' 的条目，不会误匹配其他状态。

P5 PikPak 清理幂等性：对同一条目多次执行垃圾回收，pikpak_cleaned 最终为 1，不会重复调用 PikPak 删除 API。

P6 重启恢复完整性：系统重启后，所有 downloading 和 transferring 状态的条目必须恢复轮询，不丢失任何进行中的任务。


---

## 11. 关键设计决策文档

### 11.1 阶段时间字段的 Fallback 策略

**决策：不支持 fallback，旧数据将被跳过**

**背景：**
- `download_started_at` 和 `transfer_started_at` 字段用于超时判断的基准时间
- 旧版本数据可能缺失这些字段

**决策内容：**
- `RecoverOnStartup` 在恢复 downloading/transferring 状态条目时，若发现缺失对应的阶段时间字段，将跳过该条目并记录 ERROR 日志
- 不使用 `updated_at` 作为 fallback，因为这会导致超时基准不准确
- 缺失阶段时间字段的条目需要人工介入（通过 retry 重新开始流程）

**理由：**
1. 超时判断的准确性优先于向后兼容性
2. 使用 `updated_at` 作为 fallback 会导致超时基准漂移（每次状态更新都会重置基准）
3. 旧数据量有限，人工介入成本可接受

**影响范围：**
- `internal/state/machine.go` 的 `RecoverOnStartup` 方法
- `internal/downloader/pikpak.go` 的 `ResumePolling` 方法
- `internal/transfer/coordinator.go` 的 `ResumePolling` 方法（未来实现）

### 11.2 Submit() 状态一致性窗口处理

**决策：通过恢复机制处理，不引入分布式事务**

**背景：**
- PikPak/Transfer 的 Submit() 流程中，远程任务创建成功后，本地状态转换可能失败
- 这会导致短暂的状态不一致：远程任务存在，但本地状态未更新

**决策内容：**
- 不引入分布式事务或补偿事务框架
- 依赖现有的恢复机制处理：
  1. 若状态转换失败，条目保持原状态（found/downloaded）
  2. 下次调度时会重新调用 Submit()
  3. Submit() 的幂等检查会发现 task_id 为空，重新创建任务
  4. PikPak 侧可能存在重复任务，但不影响正确性（最终只有一个任务会完成）

**理由：**
1. 分布式事务复杂度高，不适合单体应用
2. 状态转换失败是极低概率事件（数据库写入失败）
3. 重复任务的副作用可接受（PikPak 空间占用，可通过 GC 清理）
4. 系统最终一致性优先于强一致性

**影响范围：**
- `internal/downloader/pikpak.go` 的 `Submit` 方法（已添加注释说明）
- `internal/transfer/coordinator.go` 的 `Submit` 方法（未来实现）

### 11.3 异常处理中的状态回滚策略

**决策：允许状态回滚到 pending，但仅限特定场景**

**背景：**
- 某些异常情况下，条目已进入中间状态（如 searching），但操作未真正执行
- 需要决策是转为 failed 还是回滚到 pending

**决策内容：**
- **允许回滚的场景**（转回 pending）：
  1. Prowlarr 不可达/超时（网络异常，搜索未执行）
  2. 资源保存失败（数据库异常，搜索结果未持久化）
- **不允许回滚的场景**（转为 failed）：
  1. 搜索成功但无结果（permanent failure）
  2. 所有结果被编码过滤（permanent failure）
  3. PikPak 提交失败（retryable failure，但已尝试过提交）
  4. 下载/转存超时（retryable failure，但已执行过操作）

**理由：**
1. 回滚到 pending 的条件：操作未真正执行 + 异常可恢复
2. 转为 failed 的条件：操作已执行 + 结果明确（无论成功失败）
3. 审计日志会记录所有状态变更，包括回滚操作

**影响范围：**
- `internal/searcher/prowlarr.go` 的 `Search` 方法
- `validTransitions` 中 `StatusSearching` 允许转换到 `StatusPending`

### 11.4 Token 刷新失败的处理策略

**决策：写回配置失败只告警，不阻断主流程**

**背景：**
- Bangumi/Trakt token 刷新后需要写回 `config.yaml`
- 写回可能失败（文件权限、磁盘满、配置来自环境变量等）

**决策内容：**
- Token 刷新成功后，内存中的 token 立即更新
- 尝试写回 `config.yaml`，失败时：
  1. 记录 WARN 日志
  2. 不返回错误，不阻断主流程
  3. 当次请求继续使用内存中的新 token
- 若配置来自环境变量（`TARO_*`），写回文件本身无意义

**理由：**
1. Token 已在内存中更新，当次请求可正常进行
2. 下次重启时，若配置文件未更新，会重新触发 token 刷新（OAuth2 refresh_token 仍有效）
3. 写回失败不应影响系统可用性

**影响范围：**
- `internal/poller/bangumi.go` 的 token 刷新逻辑
- `internal/poller/trakt.go` 的 token 刷新逻辑
- `internal/config/config.go` 的 `UpdateBangumiToken` 和 `UpdateTraktToken` 方法

### 11.5 资源表的存储策略

**决策：存储所有搜索结果（含被过滤项），用 eligible 字段区分**

**背景：**
- 用户需要了解为什么某些资源被过滤（如 AV1 编码）
- 只写日志不够持久，重启后信息丢失

**决策内容：**
- `resources` 表存储所有搜索结果，包括被编码过滤的资源
- 使用 `eligible` 字段区分：
  - `eligible=1`：可选资源，参与自动选择和 UI 展示
  - `eligible=0`：被过滤资源，不参与自动选择，但保留记录
- 被过滤资源的 `rejected_reason` 字段记录过滤原因（如 `"codec_excluded:av1"`）
- 自动选择和 UI 展示只使用 `eligible=1` 的资源

**理由：**
1. 用户可通过 WebUI 查看所有搜索结果，了解过滤原因
2. 便于调试和优化搜索策略（如调整编码过滤规则）
3. 存储成本低（每个条目通常只有几十个资源）

**影响范围：**
- `internal/db/schema.sql` 的 `resources` 表定义
- `internal/searcher/prowlarr.go` 的 `Search` 方法
- `internal/searcher/prowlarr.go` 的 `selectBestResource` 方法（只选择 eligible=1 的资源）

### 11.6 pikpak_file_path 字段语义

**决策：只存储 PikPak 内部路径，不含 `pikpak:` 前缀**

**背景：**
- rclone 使用 `pikpak:/path/to/file` 格式访问 PikPak 文件
- 需要明确 `pikpak_file_path` 字段存储的是完整路径还是裸路径

**决策内容：**
- `pikpak_file_path` 只存储 PikPak 内部路径，不含 `pikpak:` 前缀
- 示例：`/downloads/进撃の巨人/episode01.mkv`
- transfer 服务使用时拼接为：`rclone copy "pikpak:/downloads/进撃の巨人/episode01.mkv" "onedrive:{target}"`
- 即：transfer 服务负责添加 remote 前缀，字段本身只存路径部分

**理由：**
1. 路径本身是平台无关的，不应包含 rclone remote 前缀
2. 便于未来切换到其他转存方案（不依赖 rclone）
3. 数据库字段语义更清晰（存储的是文件路径，而非 rclone 特定格式）

**当前状态：**
- `internal/downloader/pikpak.go` 使用 `pikpaktui offline status <task_id> --json` 获取任务状态
- 任务完成后，`pikpakTaskStatus.Name` 字段作为文件路径（pikpaktui 返回文件名）
- transfer 服务使用时拼接为 `pikpak:{name}` 作为 rclone 源路径

**影响范围：**
- `internal/downloader/pikpak.go` 的 `handleTaskCompleted` 方法
- `internal/transfer/coordinator.go` 的 `Submit` 方法（未来实现）

### 11.7 Prowlarr 不可达时的降级策略

**决策：条目保持 pending 状态，不转为 failed**

**背景：**
- Prowlarr 不可达可能是临时网络问题或服务重启
- 需要决策是转为 failed（需要人工重试）还是保持 pending（自动重试）

**决策内容：**
- Prowlarr 不可达/超时/HTTP 错误时：
  1. 条目从 searching 转回 pending
  2. 记录 WARN 日志
  3. 下次调度时自动重试
- 搜索成功但无结果时：
  1. 条目转为 failed（permanent）
  2. 需要人工介入（可能是标题错误或资源确实不存在）

**理由：**
1. Prowlarr 不可达是临时性问题，自动重试成功率高
2. 减少人工介入次数，提高系统自动化程度
3. 审计日志会记录 searching -> pending 的转换，便于排查问题

**影响范围：**
- `internal/searcher/prowlarr.go` 的 `Search` 方法
- `validTransitions` 中 `StatusSearching` 允许转换到 `StatusPending`

### 11.8 资源保存失败的处理策略

**决策：转回 pending 而非 failed，保留搜索结果供重试**

**背景：**
- 资源保存失败通常是数据库临时异常（锁冲突、磁盘满等）
- 搜索结果已获取，但未持久化

**决策内容：**
- 资源保存失败时（无论是 eligibleCount==0 分支还是正常流程）：
  1. 条目从 searching 转回 pending
  2. 记录 ERROR 日志
  3. 下次调度时重新搜索（Prowlarr 结果可能有缓存）
- 不转为 failed，因为：
  1. 搜索本身是成功的（Prowlarr 返回了结果）
  2. 失败原因是数据库异常，而非业务逻辑问题

**理由：**
1. 数据库异常通常是临时性的，重试成功率高
2. 转为 failed 会丢失"搜索成功"的信息，需要人工判断是否重试
3. 保持 pending 状态，调度器会自动重试，符合 task 要求的"保存所有搜索结果"

**影响范围：**
- `internal/searcher/prowlarr.go` 的 `Search` 方法（lines 176-194）

