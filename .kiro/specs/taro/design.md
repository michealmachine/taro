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
| PikPak SDK | `github.com/lyqingye/pikpak-go` | 现成 Go SDK，支持离线下载、文件管理、删除；若维护停滞可直接调用 PikPak REST API |
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
| PikPak | 账号密码登录获取 token | 通过 `pikpak-go` SDK 封装 |
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
    media_type      TEXT NOT NULL,             -- 'anime' | 'movie' | 'tv'
    source          TEXT NOT NULL,             -- 'bangumi' | 'trakt' | 'manual'
    source_id       TEXT NOT NULL,             -- 平台原始 ID（Bangumi subject_id / Trakt slug / manual 时为 UUID）
    season          INTEGER NOT NULL DEFAULT 1,-- 季数（电影为 0，剧集/动漫从 1 开始；创建电影条目时需显式传 0）
    status          TEXT NOT NULL DEFAULT 'pending',
    ask_mode        INTEGER NOT NULL DEFAULT 0,-- 0=全局配置 1=强制询问 2=强制自动
    resolution      TEXT,                      -- 条目级分辨率覆盖（NULL=使用全局）
    -- 搜索结果
    selected_resource_id TEXT,                 -- 选中的资源 ID（关联 resources 表）
    -- PikPak 信息
    pikpak_task_id  TEXT,                      -- PikPak 离线下载任务 ID
    pikpak_file_id  TEXT,                      -- PikPak 文件 ID
    pikpak_file_path TEXT,                     -- PikPak 文件路径
    pikpak_cleaned  INTEGER NOT NULL DEFAULT 0,-- PikPak 文件是否已清理
    -- 转存信息
    transfer_task_id TEXT,                     -- taro-transfer 任务 ID
    target_path     TEXT,                      -- OneDrive 目标路径
    -- 失败信息
    failed_stage    TEXT,                      -- 失败时所处阶段
    failed_reason   TEXT,                      -- 失败原因
    failed_at       DATETIME,
    -- 时间戳
    created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source, source_id, season)          -- 防止重复条目
);

-- 搜索候选资源表（条目进入终态后可清理关联的旧资源记录以节省空间）
CREATE TABLE resources (
    id          TEXT PRIMARY KEY,              -- UUID
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    title       TEXT NOT NULL,                 -- 资源文件名
    magnet      TEXT NOT NULL,                 -- 磁力链接
    size        INTEGER,                       -- 文件大小（字节）
    seeders     INTEGER,                       -- 做种数
    resolution  TEXT,                          -- 解析出的分辨率（'1080p'|'720p'|'other'）
    indexer     TEXT,                          -- 来源索引器名称
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 状态变更审计日志表
CREATE TABLE state_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT,                          -- 变更原因（可选）
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 索引
CREATE INDEX idx_entries_status ON entries(status);
CREATE INDEX idx_entries_source ON entries(source, source_id);
CREATE INDEX idx_resources_entry ON resources(entry_id);
CREATE INDEX idx_state_logs_entry ON state_logs(entry_id);
CREATE INDEX idx_entries_failed_at ON entries(failed_at) WHERE status = 'failed';
```

### 3.2 状态枚举

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

// 合法状态转换表
var validTransitions = map[EntryStatus][]EntryStatus{
    StatusPending:        {StatusSearching},
    StatusSearching:      {StatusFound, StatusNeedsSelection, StatusFailed},
    StatusFound:          {StatusDownloading},
    StatusDownloading:    {StatusDownloaded, StatusFailed},
    StatusDownloaded:     {StatusTransferring},
    StatusTransferring:   {StatusTransferred, StatusFailed},
    StatusTransferred:    {StatusInLibrary},
    StatusNeedsSelection: {StatusFound, StatusCancelled},
    StatusFailed:         {StatusPending, StatusDownloaded},
    // 任意非终态可转为 cancelled
}
```

---

## 4. 核心模块设计

### 4.1 状态机模块（state/machine.go）

状态机是整个系统的核心，所有状态变更必须通过它进行。

```go
type StateMachine struct {
    db *sqlx.DB
}

// Transition 执行状态转换，写入审计日志
func (sm *StateMachine) Transition(ctx context.Context, entryID string, to EntryStatus, reason string) error

// TransitionWithUpdate 状态转换同时更新其他字段
func (sm *StateMachine) TransitionWithUpdate(ctx context.Context, entryID string, to EntryStatus, updates map[string]any) error

// RecoverOnStartup 系统启动时执行恢复逻辑
func (sm *StateMachine) RecoverOnStartup(ctx context.Context) error
```

**RecoverOnStartup 逻辑：**
1. 将所有 `searching` 状态条目重置为 `pending`
2. 查询所有 `downloading` 状态条目，将其 `pikpak_task_id` 重新注册到 Downloader 的轮询队列
3. 查询所有 `transferring` 状态条目，将其 `transfer_task_id` 重新注册到 TransferCoordinator 的轮询队列
4. `found` 和 `downloaded` 状态的条目由调度器自然恢复（每分钟触发下载/转存）

### 4.2 平台轮询模块（poller/）

两个 Poller 实现同一接口：

```go
type Poller interface {
    Poll(ctx context.Context) error
    Name() string
}
```

**BangumiPoller 流程：**
1. 调用 `GET https://api.bgm.tv/v0/users/{uid}/collections?subject_type=2&type=1`（subject_type=2 为动漫，type=1 为想看）
2. 遍历结果，对每个 subject 检查 `entries` 表是否已存在（`source='bangumi' AND source_id=?`）
3. 不存在则创建新条目，`status='pending'`，`media_type='anime'`，标题优先使用 `name`（日文原名）而非 `name_cn`（中文译名），以提高 Nyaa.si 等日文资源站的搜索成功率

**TraktPoller 流程：**
1. 调用 `GET https://api.trakt.tv/sync/watchlist?type=movies,shows`
2. 遍历结果，区分 movie/show，检查是否已存在
3. 对于 show，默认创建第一季条目（season=1）；用户可通过 WebUI/CLI 手动添加其他季，或在配置中启用"自动添加所有季"模式（需额外调用 Trakt API 查询季数）

**OAuth2 Token 刷新：**
两个 Poller 在请求前检查 token 是否过期，过期则调用 refresh 端点，刷新后通过互斥锁保护写回 `config.yaml`（避免并发写入冲突）。

### 4.3 资源搜索模块（searcher/prowlarr.go）

```go
type SearchResult struct {
    ID         string
    Title      string
    Magnet     string
    Size       int64
    Seeders    int
    Resolution string  // 解析自标题
    Indexer    string
}

type Searcher struct {
    prowlarrURL string
    apiKey      string
    db          *sqlx.DB
    sm          *StateMachine
}

func (s *Searcher) Search(ctx context.Context, entry *Entry) error
```

**Search 流程：**
1. 将条目状态转为 `searching`
2. 构造搜索关键词：
   - 剧集/动漫：`{title} S{season:02d}`（对于 Bangumi 动漫，title 使用日文原名 `name` 而非中文译名 `name_cn`）
   - 电影：`{title} {year}`
3. 调用 Prowlarr `GET /api/v1/search`，根据 `media_type` 指定 `indexerIds`
4. 解析结果，从标题中提取分辨率（正则匹配 `1080p|1080i|720p|480p`）
5. 将所有候选资源写入 `resources` 表
6. 根据询问模式决策：
   - 无结果 → `failed`
   - 有结果且询问模式关闭 → 按分辨率优先级选最佳 → `found`
   - 有结果且询问模式开启 → `needs_selection`，触发 Notifier 发送 TG 消息

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
type Downloader struct {
    client *pikpak.Client
    db     *sqlx.DB
    sm     *StateMachine
    // 轮询队列：entry_id -> pikpak_task_id
    polling sync.Map
}

func (d *Downloader) Submit(ctx context.Context, entry *Entry) error
func (d *Downloader) StartPolling(ctx context.Context)
func (d *Downloader) ResumePolling(ctx context.Context, entryID, taskID string)
```

**Submit 流程：**
1. 调用 `pikpak.Client.OfflineDownload(magnet)` 提交离线下载
2. 记录 `pikpak_task_id` 到数据库（事务保证）
3. 状态转为 `downloading`
4. 将 `(entryID, taskID)` 加入轮询队列（sync.Map 内存存储，重启后由 RecoverOnStartup 恢复）

**轮询逻辑（goroutine）：**
- 每隔配置的间隔（默认 5 分钟）遍历轮询队列
- 调用 `pikpak.Client.OfflineList()` 批量查询任务状态
- 完成 → 记录 `pikpak_file_id` 和 `pikpak_file_path`，状态转为 `downloaded`，从队列移除
- 失败 → 状态转为 `failed`，从队列移除

### 4.5 转存协调模块（transfer/coordinator.go）

```go
type TransferCoordinator struct {
    transferURL string
    authToken   string
    db          *sqlx.DB
    sm          *StateMachine
    // 轮询队列：entry_id -> transfer_task_id
    polling sync.Map
}

func (tc *TransferCoordinator) Submit(ctx context.Context, entry *Entry) error
func (tc *TransferCoordinator) StartPolling(ctx context.Context)
func (tc *TransferCoordinator) ResumePolling(ctx context.Context, entryID, taskID string)
```

**目标路径生成规则：**
```
电影：  /media/movies/{title} ({year})/
剧集：  /media/tv/{title}/Season {season:02d}/
动漫：  /media/anime/{title}/Season {season:02d}/
```

**Submit 流程：**
1. 生成 `target_path`，写入数据库
2. 调用 `POST {transferURL}/transfer`，携带 `{entry_id, pikpak_file_path, target_path}`，Header 带 `Authorization: Bearer {token}`
3. 获取 `task_id`，写入数据库
4. 状态转为 `transferring`
5. 将 `(entryID, taskID)` 加入轮询队列

**轮询逻辑：**
- 每隔配置间隔（默认 2 分钟）遍历轮询队列
- 调用 `GET {transferURL}/transfer/{task_id}/status`
- `done` → 状态转为 `transferred`，从队列移除
- `failed` → 状态转为 `failed`，记录原因，从队列移除

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
2. 查询数据库中所有 `status='transferred'` 的条目（已入库的条目不会重复匹配）
3. 检查 `Path` 是否以某条目的 `target_path` 为前缀（目标路径生成规则保证不会出现包含关系）
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

### 4.9 TG Bot 模块（bot/bot.go）

```go
type Bot struct {
    bot      *tgbotapi.BotAPI
    db       *sqlx.DB
    sm       *StateMachine
    notifier *Notifier
}

func (b *Bot) Start(ctx context.Context)
func (b *Bot) handleCallbackQuery(query *tgbotapi.CallbackQuery)
func (b *Bot) handleCommand(msg *tgbotapi.Message)
```

**支持的命令：**
- `/list` - 列出所有条目
- `/pending` - 列出待选择条目
- `/add <title>` - 手动添加条目
- `/retry <id>` - 重试失败条目
- `/cancel <id>` - 取消条目

**Callback 处理：**
解析 `select:{entry_id}:{resource_index}` → 更新 `selected_resource_id` → 状态转为 `found`

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

### 7.3 OAuth2 Token 刷新

Token 在过期前 5 分钟自动刷新，刷新后通过 viper.WriteConfig() 持久化回 config.yaml。

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

| 模块 | 错误类型 | 处理策略 |
|------|----------|----------|
| Poller | API 请求失败 | 记录日志，跳过本次，下次重试 |
| Poller | Token 过期 | 自动刷新，失败则 TG 告警 |
| Searcher | Prowlarr 不可达 | 条目保持 pending，下次调度重试 |
| Searcher | 无搜索结果 | 条目转为 failed |
| Downloader | PikPak 提交失败 | 条目转为 failed |
| Downloader | 轮询超时（>24h） | 条目转为 failed |
| TransferCoordinator | taro-transfer 不可达 | 条目保持 downloaded，下次重试 |
| TransferCoordinator | 转存失败 | 条目转为 failed |
| WebhookHandler | 无法匹配条目 | 记录日志，不影响系统 |
| PlatformUpdater | 回调失败 | 记录日志，条目保持 in_library |
| Notifier | TG 消息失败 | 记录日志，不影响主流程 |

所有 goroutine 使用 defer recover() 防止 panic 导致服务崩溃。

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