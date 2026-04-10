# 需求文档

## 简介

taro 是一个运行在树莓派 Docker 环境中的个人媒体库自动化管理系统。系统的核心目标是将"想看"的意图转化为"已入库"的结果，全程自动化，减少人工干预。

完整流程：用户在 Bangumi / Trakt 收藏内容 → 系统定时拉取收藏 → 通过 Prowlarr 搜索资源 → PikPak 离线下载 → 触发 taro-transfer（HuggingFace Space）执行 rclone 转存到 OneDrive → 树莓派挂载 OneDrive → Jellyfin 刮削入库 → webhook 回调更新平台状态。

系统由两个子项目组成：
- **taro**：主服务，运行在树莓派，负责核心调度、状态机、WebUI、TG Bot、CLI
- **taro-transfer**：极简 HTTP 服务，运行在 HuggingFace Space，负责 rclone 转存

### 范围说明

**本版本包含的功能：**
- 自动化的媒体内容发现、下载、转存、入库流程
- 多端交互（WebUI、CLI、Telegram Bot）
- 状态追踪与故障恢复
- 平台状态同步（Bangumi、Trakt）

**本版本不包含的功能（Out of Scope）：**
- **文件自动重命名与整理**：系统不负责将下载的文件重命名为 Jellyfin 友好的格式。用户需要：
  - 在 Prowlarr 中配置可靠的资源发布组（如动漫选择规范命名的字幕组）
  - 或依赖 Jellyfin 的高级刮削插件（如 TMDB、AniDB）进行识别
  - 未来版本可能在 `transferred` → `in_library` 之间引入 `organizing` 状态实现自动重命名
- **播放进度追踪**：系统仅在入库时更新平台状态为"拥有"（Trakt collected / Bangumi 在看），不追踪实际播放进度或标记为"看过"

---

## 词汇表

- **taro**：主服务，运行在树莓派 Docker 中
- **taro-transfer**：转存子服务，运行在 HuggingFace Space Docker 中
- **条目（Entry）**：一个待处理的媒体内容单元，粒度为"一部剧/动漫的一季"或"一部电影"。电视剧和动漫以季为单位创建条目（如《进击的巨人》第一季为一个条目），搜索整季资源包；电影以单片为一个条目。
- **状态机（State_Machine）**：管理条目生命周期的核心组件
- **Poller**：定时轮询外部平台收藏的组件
- **Searcher**：通过 Prowlarr 搜索资源的组件
- **Downloader**：通过 PikPak 执行离线下载的组件
- **Transfer_Coordinator**：主服务中负责触发转存、轮询转存状态的组件
- **Transfer_Service**：taro-transfer 服务，执行 rclone 转存
- **Webhook_Handler**：接收 Jellyfin 入库事件的组件
- **Notifier**：发送 Telegram 通知的组件
- **WebUI**：基于 Go + Templ + HTMX 的服务端渲染 Web 界面
- **CLI**：taro 命令行工具
- **TG_Bot**：Telegram Bot 交互界面
- **Bangumi**：动漫内容平台，提供"想看"状态管理，需要 OAuth2 认证
- **Trakt**：电影/剧集内容平台，提供 watchlist 管理，需要 OAuth2 认证
- **Prowlarr**：资源索引聚合器，提供搜索 API
- **PikPak**：云存储服务，支持磁力链接离线下载
- **OneDrive**：微软云存储，通过 rclone 挂载到树莓派
- **Jellyfin**：本地媒体服务器，负责刮削和播放
- **目标路径（Target_Path）**：条目在 OneDrive 中的存储路径，在转存阶段由 Transfer_Coordinator 生成并记录，用于 Jellyfin 入库匹配

---

## 需求

### 需求 1：条目状态机

**用户故事：** 作为系统，我希望用统一的状态机管理每个媒体条目的生命周期，以便追踪每个条目的处理进度并支持故障恢复。

#### 验收标准

1. THE State_Machine SHALL 为每个条目维护以下状态：`pending`、`searching`、`found`、`downloading`、`downloaded`、`transferring`、`transferred`、`in_library`、`needs_selection`、`cancelled`、`failed`
2. THE State_Machine SHALL 仅允许以下合法状态转换：
   - `pending → searching`
   - `searching → found`、`searching → needs_selection`、`searching → failed`
   - `found → downloading`
   - `downloading → downloaded`、`downloading → failed`
   - `downloaded → transferring`
   - `transferring → transferred`、`transferring → failed`
   - `transferred → in_library`（Jellyfin 确认后）
   - `needs_selection → found`（用户选择后）、`needs_selection → cancelled`（用户取消）
   - `failed → pending`（从任意阶段重试，从头开始）
   - `failed → downloaded`（仅当失败阶段为 `transferring` 且 PikPak 文件仍存在时，跳过下载直接重试转存）
   - 任意非终态 → `cancelled`（用户手动取消）
3. WHEN 条目状态转换发生，THE State_Machine SHALL 在 SQLite 数据库中记录旧状态、新状态及转换时间戳，形成完整审计日志
4. IF 条目进入 `failed` 状态，THEN THE State_Machine SHALL 记录失败原因、失败时间和失败阶段
5. THE State_Machine SHALL 将所有条目数据持久化到 SQLite 数据库
6. WHEN 系统重启时，THE State_Machine SHALL 执行以下恢复逻辑：
   - 将所有 `searching` 状态的条目重置为 `pending`
   - 重新加载所有 `downloading` 状态的条目，恢复对 PikPak API 的状态轮询
   - 重新加载所有 `transferring` 状态的条目，恢复对 Transfer_Service 状态接口的轮询

---

### 需求 2：平台收藏轮询

**用户故事：** 作为用户，我希望系统自动拉取我在 Bangumi 和 Trakt 上的收藏，以便无需手动添加就能触发下载流程。

#### 验收标准

1. THE Poller SHALL 按可配置的时间间隔（默认每日一次）轮询 Bangumi"想看"列表
2. THE Poller SHALL 按可配置的时间间隔（默认每日一次）轮询 Trakt watchlist
3. WHEN Poller 发现新条目，THE Poller SHALL 在数据库中创建对应条目并设置状态为 `pending`
4. WHEN Poller 发现条目已存在于数据库，THE Poller SHALL 跳过该条目，不创建重复记录
5. THE Poller SHALL 区分内容类型：Bangumi 条目标记为动漫，Trakt 条目标记为电影或剧集

---

### 需求 3：资源搜索

**用户故事：** 作为系统，我希望通过 Prowlarr 自动搜索媒体资源，以便找到可下载的磁力链接。

#### 验收标准

1. WHEN 调度器触发搜索任务且条目状态为 `pending`，THE Searcher SHALL 调用 Prowlarr 搜索 API 搜索对应资源，并将状态更新为 `searching`
2. THE Searcher SHALL 按分辨率优先级筛选结果：1080p > 720p > 其他
3. WHERE 条目配置了分辨率覆盖，THE Searcher SHALL 使用条目级分辨率配置替代全局默认值
4. WHEN 搜索返回唯一匹配结果，THE Searcher SHALL 自动选择该结果并将状态更新为 `found`
5. WHEN 搜索返回多个结果，THE Searcher SHALL 检查条目的询问模式配置（默认为全局配置）：IF 询问模式启用，THEN 将状态更新为 `needs_selection` 并暂停自动处理；ELSE 按分辨率优先级自动选择第一个结果并将状态更新为 `found`
6. WHEN 搜索未找到任何结果，THE Searcher SHALL 将条目状态更新为 `failed` 并记录原因
7. THE Searcher SHALL 对动漫条目优先使用 Nyaa.si 和 Mikan Project 索引器
8. THE Searcher SHALL 对电影和剧集条目优先使用 1337x 和 YTS 索引器
9. WHEN 搜索返回候选资源列表，THE Searcher SHALL 记录每个候选资源的文件名、大小、做种数和磁力链接，以便用户在询问模式下查看文件命名是否规范

---

### 需求 4：PikPak 离线下载

**用户故事：** 作为系统，我希望将磁力链接提交给 PikPak 进行离线下载，以便利用 PikPak 的云端下载能力获取资源。

#### 验收标准

1. WHEN 条目状态为 `found`，THE Downloader SHALL 通过 PikPak REST API 提交磁力链接进行离线下载，并将状态更新为 `downloading`
2. THE Downloader SHALL 按可配置的时间间隔轮询 PikPak 下载任务状态
3. WHEN PikPak 下载任务完成，THE Downloader SHALL 将条目状态更新为 `downloaded` 并记录 PikPak 文件路径
4. IF PikPak 下载任务失败，THEN THE Downloader SHALL 将条目状态更新为 `failed` 并记录错误信息

---

### 需求 5：rclone 转存（taro-transfer）

**用户故事：** 作为系统，我希望通过 taro-transfer 服务将 PikPak 中的文件转存到 OneDrive，以便树莓派可以通过挂载访问文件。

#### 验收标准

1. THE Transfer_Service SHALL 暴露 `POST /transfer` HTTP 接口，接收条目 ID、PikPak 文件路径和目标 OneDrive 路径参数；所有请求须携带预共享 token 进行认证
2. THE Transfer_Service SHALL 暴露 `GET /transfer/{task_id}/status` HTTP 接口，返回转存任务的当前状态（pending/running/done/failed）
3. WHEN `POST /transfer` 被调用且认证通过，THE Transfer_Service SHALL 异步执行 rclone copy 将文件从 PikPak 复制到 OneDrive，并返回 task_id
4. WHEN rclone 转存完成，THE Transfer_Service SHALL 更新任务状态为 `done`
5. IF rclone 转存失败，THEN THE Transfer_Service SHALL 更新任务状态为 `failed` 并记录错误原因
6. WHEN 条目状态为 `downloaded`，THE Transfer_Coordinator SHALL 生成目标 OneDrive 路径、将路径记录到数据库、调用 Transfer_Service 的 `POST /transfer` 接口，并将条目状态更新为 `transferring`
7. THE Transfer_Coordinator SHALL 按可配置的时间间隔轮询 `GET /transfer/{task_id}/status` 接口，检查转存进度
8. WHEN 轮询结果为 `done`，THE Transfer_Coordinator SHALL 将条目状态更新为 `transferred`
9. WHEN 轮询结果为 `failed`，THE Transfer_Coordinator SHALL 将条目状态更新为 `failed` 并记录错误原因
10. WHEN rclone 转存成功，THE Transfer_Service SHALL 删除 PikPak 中对应的源文件以释放空间

---

### 需求 6：Jellyfin 入库检测

**用户故事：** 作为系统，我希望通过 Jellyfin webhook 自动检测媒体入库事件，以便在文件入库后触发平台状态更新。

#### 验收标准

1. THE Webhook_Handler SHALL 暴露 `POST /webhook/jellyfin` HTTP 接口
2. WHEN Jellyfin 发送 `ItemAdded` 事件，THE Webhook_Handler SHALL 解析事件中的文件路径，并仅与数据库中 `transferred` 状态条目记录的 Target_Path 进行前缀匹配（忽略其他状态的条目）；对于剧集，首次匹配任意一集即更新状态，后续集数的 webhook 因条目已非 `transferred` 状态而自然忽略
3. WHEN 条目匹配成功，THE Webhook_Handler SHALL 将条目状态更新为 `in_library`
4. IF Webhook_Handler 无法匹配条目，THEN THE Webhook_Handler SHALL 记录未匹配事件日志，不影响系统正常运行

---

### 需求 7：平台状态回调

**用户故事：** 作为用户，我希望媒体入库后系统自动更新 Bangumi 和 Trakt 上的状态，以便平台记录与实际媒体库保持同步。

#### 验收标准

1. WHEN 动漫条目状态变为 `in_library`，THE taro SHALL 将 Bangumi 对应条目状态从"想看"更新为"在看"
2. WHEN 电影或剧集条目状态变为 `in_library`，THE taro SHALL 在 Trakt 上将对应条目标记为 collected 并从 watchlist 移除
3. IF 平台状态更新失败，THEN THE taro SHALL 记录错误日志，不影响条目的 `in_library` 状态

---

### 需求 8：询问模式与用户选择

**用户故事：** 作为用户，我希望在系统找到多个候选资源时能手动选择，以便控制下载内容的质量和来源。

#### 验收标准

1. WHERE 条目启用了询问模式，THE Searcher SHALL 在找到多个候选资源时将条目状态设置为 `needs_selection`
2. WHEN 条目进入 `needs_selection` 状态，THE Notifier SHALL 通过 Telegram Bot 发送包含候选资源列表（含文件名、大小、做种数）的 inline keyboard 消息
3. WHEN 用户通过 TG_Bot 选择资源，THE TG_Bot SHALL 记录选择结果，将条目状态更新为 `found` 并继续下载流程
4. WHEN 用户通过 WebUI 选择资源，THE WebUI SHALL 记录选择结果，将条目状态更新为 `found` 并继续下载流程
5. WHEN 用户通过 CLI 执行 `taro select` 命令，THE CLI SHALL 记录选择结果，将条目状态更新为 `found` 并继续下载流程
6. WHEN 用户通过 TG_Bot / WebUI / CLI 取消条目，THE taro SHALL 将条目状态更新为 `cancelled`
7. THE taro SHALL 在 WebUI、TG_Bot、CLI 三端共享 `needs_selection` 队列的实时状态
8. WHEN 条目在 `needs_selection` 状态停留超过可配置的超时时间（默认 24 小时），THE taro SHALL 按全局分辨率优先级自动选择最佳结果并将状态更新为 `found`，同时通过 TG_Bot 发送超时自动选择通知

---

### 需求 9：WebUI

**用户故事：** 作为用户，我希望通过 Web 界面管理媒体库和查看系统状态，以便在浏览器中完成日常操作。

#### 验收标准

1. THE WebUI SHALL 使用 Go + Templ + HTMX 实现服务端渲染，编译为单一二进制文件
2. THE WebUI SHALL 提供媒体库浏览页面，展示所有条目及其当前状态
3. THE WebUI SHALL 提供待处理队列页面，展示所有 `needs_selection` 状态的条目及候选资源
4. THE WebUI SHALL 提供手动添加条目的表单，支持输入媒体标题、类型和可选的分辨率覆盖
5. THE WebUI SHALL 提供系统状态页面，展示各组件运行状态和最近日志
6. WHEN 用户在 WebUI 手动添加条目，THE WebUI SHALL 在数据库中创建条目并设置状态为 `pending`

---

### 需求 10：CLI

**用户故事：** 作为用户，我希望通过命令行工具管理媒体库，以便在终端环境中执行常用操作。

#### 验收标准

1. THE CLI SHALL 提供 `taroctl list` 命令，输出所有条目及其状态
2. THE CLI SHALL 提供 `taroctl add <title>` 命令，手动添加条目到处理队列
3. THE CLI SHALL 提供 `taroctl pending` 命令，列出所有 `needs_selection` 状态的条目
4. THE CLI SHALL 提供 `taroctl select <entry_id> <resource_index>` 命令，为指定条目选择资源
5. THE CLI SHALL 提供 `taroctl cancel <entry_id>` 命令，将指定条目状态更新为 `cancelled`
6. THE CLI SHALL 提供 `taroctl retry <entry_id>` 命令，重试失败条目
7. THE CLI SHALL 提供 `taroctl retry --all` 命令，批量重试所有失败条目
8. WHEN `taroctl add` 执行成功，THE CLI SHALL 通过 HTTP API 在数据库中创建条目并设置状态为 `pending`

---

### 需求 11：Telegram Bot

**用户故事：** 作为用户，我希望通过 Telegram Bot 接收通知并处理待选择队列，以便在移动端及时响应系统事件。

#### 验收标准

1. WHEN 新条目被添加到处理队列，THE Notifier SHALL 通过 TG_Bot 发送通知消息
2. WHEN 条目进入 `needs_selection` 状态，THE TG_Bot SHALL 发送包含候选资源列表的 inline keyboard 消息供用户选择
3. WHEN 条目状态变为 `in_library`，THE Notifier SHALL 通过 TG_Bot 发送入库完成通知
4. WHEN 用户通过 TG_Bot inline keyboard 选择资源，THE TG_Bot SHALL 将选择结果同步到数据库并更新条目状态为 `found`
5. IF TG_Bot 消息发送失败，THEN THE Notifier SHALL 记录错误日志，不影响主流程继续执行

---

### 需求 12：配置管理

**用户故事：** 作为运维人员，我希望通过配置文件管理系统参数，以便在不修改代码的情况下调整系统行为。

#### 验收标准

1. THE taro SHALL 使用 YAML 格式配置文件（默认路径 `config.yaml`），支持通过 `--config` 参数指定路径
2. THE taro SHALL 支持通过环境变量覆盖所有配置项（环境变量优先级高于配置文件），以适应 Docker 部署场景
3. THE taro SHALL 支持配置 Bangumi 轮询间隔（默认 24 小时）
4. THE taro SHALL 支持配置 Trakt 轮询间隔（默认 24 小时）
5. THE taro SHALL 支持配置 PikPak 下载状态轮询间隔（默认 5 分钟）
6. THE taro SHALL 支持配置全局默认分辨率优先级（默认 1080p > 720p > 其他）
7. THE taro SHALL 支持配置 Prowlarr API 地址和 API Key
8. THE taro SHALL 支持配置 PikPak 账号（用户名/密码）
9. THE taro SHALL 支持配置 Telegram Bot Token 和目标 Chat ID
10. THE taro SHALL 支持配置 taro-transfer 服务地址和预共享认证 token
11. THE taro SHALL 支持配置全局询问模式开关（默认关闭）
12. THE taro SHALL 支持配置 needs_selection 超时时间（默认 24 小时）
13. THE taro SHALL 支持配置 Transfer_Coordinator 轮询间隔（默认 2 分钟）
14. THE taro SHALL 支持配置 Bangumi 用户 ID (uid)、OAuth2 access token 和 refresh token
15. THE taro SHALL 支持配置 Trakt OAuth2 access token、refresh token 和 client credentials
16. WHEN OAuth2 token 过期，THE taro SHALL 使用 refresh token 自动刷新并持久化新 token 到配置文件

---

### 需求 13：失败重试机制

**用户故事：** 作为用户，我希望系统支持对失败条目进行精准重试，以便处理临时性错误（如网络波动、API 限流）而不造成重复下载。

#### 验收标准

1. WHEN 条目进入 `failed` 状态，THE taro SHALL 记录失败原因、失败时间和失败阶段
2. THE WebUI SHALL 在条目详情页提供"重试"按钮，根据失败阶段智能选择重试起点：若失败阶段为 `transferring` 且 PikPak 文件仍存在，则重置为 `downloaded`；否则重置为 `pending`
3. THE CLI SHALL 提供 `taroctl retry <entry_id>` 命令，执行与 WebUI 相同的智能重试逻辑
4. THE CLI SHALL 提供 `taroctl retry --all` 命令，对所有 `failed` 状态条目执行批量智能重试
5. THE TG_Bot SHALL 在发送失败通知时提供 inline keyboard"重试"按钮，执行智能重试逻辑
6. WHEN 条目执行智能重试，THE State_Machine SHALL 根据目标状态（`pending` 或 `downloaded`）清除对应阶段之后的历史记录并重新进入处理流程


---

### 需求 14：OneDrive 挂载健康检测

**用户故事：** 作为用户，我希望系统能检测 OneDrive 挂载状态，以便在挂载断开时及时告警，避免 Jellyfin 无法访问媒体文件。

#### 验收标准

1. THE taro SHALL 按可配置的时间间隔（默认 10 分钟）检测 OneDrive 挂载点是否可访问
2. WHEN 挂载点检测失败，THE Notifier SHALL 通过 TG_Bot 发送挂载断开告警通知
3. WHEN 挂载点恢复可访问，THE Notifier SHALL 通过 TG_Bot 发送挂载恢复通知
4. THE WebUI SHALL 在系统状态页面展示 OneDrive 挂载点的当前健康状态
5. THE taro SHALL 支持通过配置文件指定 OneDrive 挂载点路径

---

### 需求 15：PikPak 空间垃圾回收

**用户故事：** 作为用户，我希望系统能自动清理 PikPak 中失败或取消任务的残留文件，以便避免 PikPak 有限空间被占满。

#### 验收标准

1. THE taro SHALL 按可配置的时间间隔（默认每日一次）执行 PikPak 垃圾回收任务
2. WHEN 垃圾回收任务执行时，THE taro SHALL 查找所有满足以下条件的条目：
   - 状态为 `failed` 且失败时间超过可配置的保留期（默认 7 天）
   - 状态为 `cancelled`
3. FOR EACH 符合条件的条目，THE taro SHALL 调用 PikPak API 删除对应的文件或任务记录
4. WHEN 删除成功，THE taro SHALL 在条目记录中标记 PikPak 文件已清理
5. IF 删除失败（如文件已不存在），THE taro SHALL 记录日志但不影响垃圾回收流程继续执行
6. THE taro SHALL 支持通过配置文件设置 PikPak 垃圾回收间隔和失败条目保留期
