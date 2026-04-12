# 实现计划：taro 媒体库自动化系统

## 概述

本实现计划将 taro 系统分为三个主要阶段：基础设施搭建、核心业务模块实现、交互界面开发。采用增量式开发策略，每个模块完成后立即集成测试，确保在树莓派资源受限环境下稳定运行。

## 任务列表

- [ ] 1. 基础设施搭建
  - [x] 1.1 初始化项目结构和依赖管理
    - 创建 `cmd/taro/main.go` 和 `cmd/taroctl/main.go` 入口文件
    - 创建 `internal/` 目录结构（config、db、state、poller、searcher、downloader、transfer、webhook、platform、notifier、bot、web、scheduler、health）
    - 初始化 `go.mod`，添加核心依赖：`modernc.org/sqlite`、`github.com/jmoiron/sqlx`、`github.com/a-h/templ`、`github.com/spf13/viper`、`github.com/robfig/cron/v3`、`github.com/lyqingye/pikpak-go`、`github.com/go-telegram-bot-api/telegram-bot-api`
    - 创建 `config.yaml.example` 配置文件模板
    - _需求：12_

  - [x] 1.2 实现配置管理模块（internal/config/config.go）
    - 使用 viper 加载 YAML 配置文件
    - 实现环境变量覆盖逻辑（TARO_ 前缀）
    - 定义配置结构体，包含所有需求 12 中列出的配置项，以及：
      - `logging.level` 和 `logging.format`（使用 log/slog）
      - `defaults.max_concurrent_searches`（调度器并发控制）
      - `retention.state_logs_days` 和 `retention.clean_resources_on_complete`
    - 实现 OAuth2 token 自动刷新后的配置持久化（WriteConfig，互斥锁保护）
    - 添加配置验证逻辑（必填项检查）
    - _需求：12_

  - [x] 1.3 实现数据库模块（internal/db/）
    - 创建 `schema.sql`，定义 entries、resources、state_logs 三张表及索引
    - entries 表包含：year、阶段时间字段（search_started_at、download_started_at、transfer_started_at）、failure_kind、**failure_code**
    - resources 表包含：codec、eligible（区分可选/被过滤）、score、selected、rejected_reason 字段
    - state_logs 表包含：metadata JSON 字段（v2 候选，当前预留）
    - 实现 `db.go`：数据库连接初始化（modernc.org/sqlite）、自动迁移逻辑
    - 实现 `entry.go`：Entry CRUD 操作（Create、Get、Update、List、ListByStatus）
    - 实现 Resource CRUD 操作（BatchCreate、ListByEntry、ListEligibleByEntry、Delete）
    - 实现 StateLog 写入操作（Create、ListByEntry）
    - 确保所有数据库操作使用事务保护
    - _需求：1.5_

  - [x] 1.4 实现状态机核心模块（internal/state/machine.go）
    - 定义 EntryStatus 枚举和 validTransitions 转换表
    - 定义 FailureKind、FailureCode 枚举；实现 FailureKindOf(code) 自动推导 kind
    - 实现 `Transition` 方法：验证转换合法性、更新条目状态、写入审计日志
    - 实现 `TransitionWithUpdate` 方法：状态转换同时更新其他字段；**自动设置阶段开始时间**（转换到 searching/downloading/transferring 时分别写入对应字段）
    - 实现 `TransitionToFailed` 方法：接受 FailureCode，自动推导 failure_kind，持久化 failure_code 到数据库
    - 实现 `RecoverOnStartup` 方法：重置 searching 状态；通过回调恢复 downloading/transferring 轮询队列（传入 download_started_at/transfer_started_at 作为超时基准）
    - 添加互斥锁保护并发状态转换；使用 slog 统一日志（不使用 fmt.Printf）
    - _需求：1.1, 1.2, 1.3, 1.4, 1.6_

- [ ] 2. 核心业务模块实现

  - [ ] 2.1 实现平台轮询模块（internal/poller/）
    - [x] 2.1.1 实现 Bangumi 轮询器（bangumi.go）
      - 启动时通过 `GET /v0/me` 获取当前用户的 uid（若配置中未提供）
      - 实现 OAuth2 token 刷新逻辑（互斥锁保护配置写入）
      - 调用 Bangumi API 获取"想看"列表（`GET /v0/users/{uid}/collections?subject_type=2&type=1`）
      - 解析响应，提取 subject_id 和日文原名（name 字段，优先于 name_cn）
      - 检查条目是否已存在（source='bangumi' AND source_id=?）
      - 创建新条目（media_type='anime', status='pending', season=1）
      - _需求：2.1, 2.4, 2.5_

    - [x] 2.1.2 实现 Trakt 轮询器（trakt.go）
      - 实现 OAuth2 token 刷新逻辑
      - 调用 Trakt API 获取 watchlist（type=movies,shows）
      - 区分 movie 和 show，解析标题和年份
      - 对 show 默认创建第一季条目（season=1）
      - 检查去重并创建新条目
      - _需求：2.2, 2.4, 2.5_

  - [x] 2.2 实现资源搜索模块（internal/searcher/prowlarr.go）
    - 实现 `Search` 方法：将条目状态转为 searching（StateMachine 自动写入 search_started_at）
    - 构造搜索关键词（Bangumi 动漫直接用标题，Trakt 动漫/剧集加 S{season:02d}，电影加年份）
    - 调用 Prowlarr API（`GET /api/v1/search`）
    - **严格区分异常 vs 无结果**：
      - Prowlarr 不可达/超时/HTTP 错误 → 条目保持 pending（不转 failed），记录日志
      - 搜索成功但无结果 → TransitionToFailed（code=no_resources，permanent）
      - 所有结果被编码过滤 → TransitionToFailed（code=all_codecs_excluded，permanent）
    - 解析搜索结果，提取分辨率（正则）和编码（正则），过滤排除的编码
    - 将**所有**搜索结果写入 resources 表（含被过滤项）：
      - 可选资源：eligible=1，计算 score
      - 被过滤资源：eligible=0，写入 rejected_reason（如 "codec_excluded:av1"）
    - 自动选择时只从 eligible=1 的资源中选择
    - 根据询问模式决策：无可选资源→failed，有资源且自动模式→found，有资源且询问模式→needs_selection
    - _需求：3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9_

  - [x] 2.3 实现 PikPak 下载管理模块（internal/downloader/pikpak.go）
    - 通过 `pikpaktui` CLI（`exec.Command`）调用 PikPak，认证通过 PIKPAK_USER/PIKPAK_PASS 环境变量传入
    - 实现 `Submit` 方法（幂等策略）：
      - 检查 entry.pikpak_task_id 是否已存在，若存在先查询 PikPak 确认任务是否活跃
      - 活跃则直接加入轮询队列（使用 download_started_at 作为超时基准），不新建任务
      - 创建远程任务（先创建远程，再通过 StateMachine 转为 downloading）
      - StateMachine 转为 downloading 时自动写入 download_started_at（不预占位）
      - 创建失败时通过 TransitionToFailed（code=service_unreachable）
    - 实现 `StartPolling` goroutine：从 config.pikpak.poll_interval 读取间隔（解析失败记录 WARN 日志，使用默认 60s）
    - 任务完成时记录 pikpak_file_id 和 pikpak_file_path（**仅存 PikPak 内部路径，不含 pikpak: 前缀**），状态转为 downloaded
    - 任务失败时通过 TransitionToFailed（code=service_unreachable，retryable）
    - 实现 `ResumePolling` 方法：使用 entry.download_started_at 作为超时基准（不使用 updated_at 或 time.Now()）
    - 超时检测（>24h）：TransitionToFailed（code=pikpak_timeout，retryable）
    - Stop() 使用 sync.Once 防止 double-close
    - _需求：4.1, 4.2, 4.3, 4.4_

  - [x] 2.4 实现转存协调模块（internal/transfer/coordinator.go）
    - 实现目标路径生成逻辑（含路径规范化：统一 `/` 分隔符、末尾带 `/`、特殊字符替换为 `_`）
    - 实现 `Submit` 方法（幂等策略）：
      - 检查 transfer_task_id 是否已存在，若存在先调用 GET /transfer/{id}/status
      - 返回 pending/running/done/failed → 按状态处理，不重新提交
      - 返回 not_found → 继续创建新任务
      - 调用 POST /transfer 创建任务；失败（服务不可达）→ 条目保持 downloaded，下次调度重试
      - 通过 StateMachine 转为 transferring，同时记录 transfer_task_id（StateMachine 自动写入 transfer_started_at）
    - 实现 `StartPolling` goroutine：定时查询 `GET /transfer/{task_id}/status`
      - done → 转为 transferred
      - failed → TransitionToFailed（code=transfer_timeout 或 service_unreachable，retryable）
      - not_found → 视为 HF Space 重启，重新提交任务（更新 transfer_task_id，条目保持 transferring）
    - 实现 `ResumePolling` 方法：使用 entry.transfer_started_at 作为超时基准
    - 处理 taro-transfer 服务不可达时的降级：条目保持 downloaded 状态，下次调度重试
    - _需求：5.6, 5.7, 5.8, 5.9_

  - [x] 2.5 实现动作服务层（internal/service/action.go）
    - 实现 `ActionService`：封装 RetryEntry、CancelEntry、SelectResource、AddEntry 四个核心动作
    - RetryEntry：检查 failure_kind，permanent 类型直接返回错误（不执行重试）；retryable 类型根据 failed_stage 决定重试起点（智能重试）
    - CancelEntry：通过 StateMachine 转为 cancelled（TransitionToFailed 中 code=user_cancelled）
    - SelectResource：验证 resource 属于该 entry 且 eligible=1；通过 StateMachine 转为 found，记录 selected_resource_id，标记 resource.selected=1
    - AddEntry：创建 manual 来源条目（source_id 使用 UUID）
    - 所有动作通过 StateMachine 执行，不直接操作数据库状态字段
    - _需求：13.1, 13.6_

  - [x] 2.6 实现 Jellyfin Webhook 处理模块（internal/webhook/jellyfin.go）
    - 定义 JellyfinItemAddedPayload 结构体（NotificationType、ItemType、Path）
    - 实现 `HandleJellyfin` HTTP handler：解析 POST 请求 body
    - 查询所有 status='transferred' 的条目
    - 实现 `normalizePath` 函数：统一 `/` 分隔符、转小写、末尾加 `/`、去除连续 `//`
    - 匹配逻辑：`strings.HasPrefix(normalizePath(webhookPath), normalizePath(entry.TargetPath))`（**必须 normalize 两侧，禁止原始字符串比较**）
    - 匹配成功时状态转为 in_library，触发平台回调
    - 无法匹配时记录日志，返回 200 OK（Jellyfin 不可达不影响系统）
    - _需求：6.1, 6.2, 6.3, 6.4_

  - [x] 2.7 实现平台状态回调模块（internal/platform/）
    - [x] 2.7.1 实现 Bangumi 回调（bangumi.go）
      - 实现 `MarkOwned` 方法：调用 `POST /v0/users/-/collections/{subject_id}`，body 设置 type=3（在看）
      - 处理 OAuth2 token 刷新
      - 失败时记录日志，不影响条目状态
      - _需求：7.1, 7.3_

    - [x] 2.7.2 实现 Trakt 回调（trakt.go）
      - 实现 `MarkOwned` 方法：调用 `POST /sync/collection` 标记为 collected
      - 调用 `DELETE /sync/watchlist/remove` 从 watchlist 移除
      - 处理 OAuth2 token 刷新
      - 失败时记录日志，不影响条目状态
      - _需求：7.2, 7.3_

  - [x] 2.8 实现通知模块（internal/notifier/telegram.go）
    - 初始化 telegram-bot-api 客户端
    - 实现 `NotifyNewEntry`：发送新条目添加通知
    - 实现 `NotifyNeedsSelection`：发送候选资源列表 inline keyboard 消息（格式：`select:{entry_id}:{resource_index}`）
    - 实现 `NotifyInLibrary`：发送入库完成通知
    - 实现 `NotifyFailed`：发送失败通知（包含失败原因和重试按钮）
    - 实现 `NotifyMountDown` 和 `NotifyMountUp`：OneDrive 挂载状态告警
    - 所有发送失败仅记录日志，不抛出错误
    - _需求：8.2, 11.1, 11.2, 11.3, 11.5, 14.2, 14.3_

  - [x] 2.9 实现 OneDrive 挂载健康检测模块（internal/health/onedrive.go）
    - 实现 `CheckMount` 方法：使用 rclone lsd 检查挂载点是否可访问
    - 记录上一次健康状态，状态变化时触发通知
    - 实现定时检测 goroutine（默认 10 分钟间隔）
    - _需求：14.1, 14.2, 14.3, 14.4, 14.5_

  - [x] 2.10 实现 PikPak 垃圾回收与数据清理模块（internal/downloader/gc.go）
    - 实现 `RunGC` 方法：查询所有符合清理条件的条目（failed 且超过保留期、或 cancelled）
    - 调用 pikpaktui CLI 删除文件（`pikpaktui rm <file_id>`）
    - 删除成功时标记 pikpak_cleaned=1
    - 删除失败时记录日志，继续处理下一个
    - 实现定时执行 goroutine（默认每日一次）
    - 同时清理终态条目（in_library、cancelled）关联的旧 resources 记录（根据 retention.clean_resources_on_complete 配置）
    - 清理超过保留期的 state_logs 记录（根据 retention.state_logs_days 配置，0=永久保留）
    - _需求：15.1, 15.2, 15.3, 15.4, 15.5, 15.6_

- [ ] 3. taro-transfer 子服务实现

  - [x] 3.1 实现 taro-transfer 服务（taro-transfer/）
    - 创建 `main.go`：初始化 HTTP 服务器（监听 7860 端口）
    - 创建 `task.go`：定义任务状态管理（sync.Map 存储 task_id -> TaskState）
      - TaskState 包含：status（pending/running/done/failed）、error_message、created_at
    - 创建 `handler.go`：
      - `POST /transfer`：验证 token、生成 task_id、异步执行 rclone copy、返回 task_id
      - `GET /transfer/{task_id}/status`：返回任务状态
        - 任务存在：返回 `{"status": "pending|running|done|failed", "error": "..."}`
        - 任务不存在：返回 `{"status": "not_found"}`（**明确信号，供主服务判断任务是否丢失**）
    - 实现 rclone 调用逻辑：`rclone copy "pikpak:{source_path}" "onedrive:{target_path}"`（source_path 来自请求，不含 pikpak: 前缀，由 transfer 服务添加）
    - 转存成功后调用 rclone delete 删除 PikPak 源文件
    - 失败时记录错误原因到 TaskState.error_message
    - 创建 `Dockerfile`：安装 rclone、复制 rclone.conf、暴露 7860 端口
    - _需求：5.1, 5.2, 5.3, 5.4, 5.5, 5.10_

- [ ] 4. 调度器与主服务集成

  - [x] 4.1 实现调度器模块（internal/scheduler/scheduler.go）
    - 使用 robfig/cron 初始化调度器
    - 注册定时任务：
      - 每分钟：处理 pending 条目（触发搜索）
      - 每分钟：处理 found 条目（触发下载）
      - 每分钟：处理 downloaded 条目（触发转存）
      - 每 30 分钟：检查 needs_selection 超时（超时自动选择最佳资源）
      - 可配置间隔：Bangumi 轮询、Trakt 轮询
      - 可配置间隔：OneDrive 健康检测
      - 可配置间隔：PikPak 垃圾回收与数据清理
    - 实现信号量限制并发数（从 config.defaults.max_concurrent_searches 读取，默认 3）
    - 实现任务跳过逻辑（上一轮未完成时跳过本次）
    - _需求：1.1, 2.3, 2.4, 3.1, 4.1, 5.6, 8.8, 14.1, 15.1_

  - [ ] 4.2 实现主服务入口（cmd/taro/main.go）
    - [ ] 4.2.1 配置加载与日志初始化
      - 加载配置（支持 --config 参数）
      - **按模块惰性校验**（不做一刀切必填）：
        - 核心必填（缺失则启动失败）：server.port、server.db_path、prowlarr.url、prowlarr.api_key、pikpak.username、pikpak.password、transfer.url、transfer.token
        - 模块可选（缺失则跳过该模块，记录 WARN）：bangumi.access_token、trakt.client_id、telegram.bot_token、onedrive.mount_path
      - 初始化 log/slog（根据 logging.level 和 logging.format 配置）
      - _需求：12.1, 12.2_
    
    - [ ] 4.2.2 依赖注入与模块初始化
      - 初始化数据库连接和自动迁移（使用简单的 CREATE TABLE IF NOT EXISTS 策略）
      - 初始化状态机并执行 RecoverOnStartup
      - 按顺序初始化所有业务模块：
        1. Notifier（其他模块依赖它发送通知）
        2. Poller（Bangumi、Trakt）
        3. Searcher
        4. Downloader（初始化 PikPak 客户端，处理登录态过期重新登录）
        5. TransferCoordinator
        6. WebhookHandler
        7. PlatformUpdater（Bangumi、Trakt）
        8. Health
        9. GC
      - _需求：1.6_
    
    - [ ] 4.2.3 启动后台服务
      - 启动调度器
      - 启动 Downloader 和 TransferCoordinator 的轮询 goroutine
      - 启动 Health 检测 goroutine
      - 启动 GC 定时任务 goroutine
      - 启动 WebUI HTTP 服务器
      - 启动 TG Bot
      - _需求：1.6_
    
    - [ ] 4.2.4 优雅关闭
      - 监听 SIGINT/SIGTERM 信号
      - 关闭顺序：
        1. 停止接收新请求（关闭 HTTP 服务器、TG Bot）
        2. 取消所有 context（停止调度器和轮询 goroutine）
        3. 等待所有 goroutine 退出（使用 sync.WaitGroup，超时 30 秒）
        4. 关闭数据库连接
      - _需求：1.6_

  - [ ] 4.3 编写核心模块单元测试
    - [ ] 4.3.1 状态机测试（state/machine_test.go）
      - 测试所有合法状态转换
      - 测试非法状态转换被拒绝
      - 测试审计日志完整性（P3 属性）
      - 测试并发状态转换的互斥锁保护
      - _验证：P1 状态转换合法性、P3 审计日志完整性_
    
    - [ ] 4.3.2 去重逻辑测试（db/entry_test.go）
      - 测试相同 (source, source_id, season) 的条目不能重复创建
      - 测试 UNIQUE 约束生效
      - _验证：P2 去重唯一性_
    
    - [ ] 4.3.3 Webhook 匹配测试（webhook/jellyfin_test.go）
      - 测试路径前缀匹配逻辑
      - 测试只匹配 transferred 状态的条目
      - 测试剧集多文件匹配（首次匹配后状态变化，后续忽略）
      - _验证：P4 Jellyfin 匹配精确性_
    
    - [ ] 4.3.4 智能重试测试（state/retry_test.go）
      - 测试 transferring 失败且文件存在 → downloaded
      - 测试 transferring 失败且文件不存在 → pending
      - 测试其他阶段失败 → pending
      - 测试历史记录清理逻辑
    
    - [ ] 4.3.5 PikPak 垃圾回收测试（downloader/gc_test.go）
      - 测试多次执行 GC 的幂等性
      - _验证：P5 PikPak 清理幂等性_

- [ ] 5. Checkpoint - 核心流程验证
  - 部署 taro-transfer 到 HuggingFace Space（或本地模拟）
  - 确保所有单元测试通过
  - 手动验证核心流程：添加条目 → 搜索 → 下载 → 转存 → 入库
  - 验证系统重启后的恢复逻辑（P6 重启恢复完整性）
  - 检查树莓派资源占用（内存 < 200MB，CPU 空闲时 < 5%）
  - 询问用户是否有问题

- [ ] 6. 交互界面开发

  - [ ] 6.1 实现 Telegram Bot 交互模块（internal/bot/bot.go）
    - 实现 `Start` 方法：启动 Bot 消息监听循环
    - 实现命令处理：`/list`、`/pending`、`/add`、`/retry`、`/cancel`
    - 所有写操作通过 ActionService 执行（不直接调用 StateMachine）
    - 实现 callback query 处理：解析 `select:{entry_id}:{resource_id}`，调用 ActionService.SelectResource
    - 实现取消按钮处理：调用 ActionService.CancelEntry
    - _需求：8.3, 8.4, 8.6, 11.4_

  - [ ] 6.2 实现 WebUI 模板和路由（internal/web/）
    - [ ] 6.2.1 创建 templ 模板文件（templates/）
      - `layout.templ`：基础布局（引入 HTMX CDN）
      - `entries.templ`：条目列表页（按状态分组，HTMX 每 30 秒自动刷新）
      - `entry_detail.templ`：条目详情页（显示状态历史、失败原因、重试按钮）
      - `pending.templ`：待选择队列页（展示候选资源列表）
      - `status.templ`：系统状态页（组件状态、OneDrive 挂载状态、最近日志）
      - `add_entry.templ`：手动添加条目表单
      - _需求：9.2, 9.3, 9.4, 9.5, 9.6_

    - [ ] 6.2.2 实现 HTTP handlers（handlers/）
      - [ ] `entries.go`：
        - GET /entries：列表页（支持按状态筛选、分页）
        - GET /entries/{id}：详情页（显示状态历史、失败原因）
        - POST /entries：手动添加条目（source='manual', source_id=UUID）
      - [ ] `actions.go`：
        - POST /entries/{id}/retry：调用 ActionService.RetryEntry
        - POST /entries/{id}/cancel：调用 ActionService.CancelEntry
        - POST /entries/{id}/select：调用 ActionService.SelectResource
      - [ ] `pending.go`：GET /pending（待选择队列）
      - [ ] `status.go`：GET /status（系统状态、OneDrive 挂载状态、最近日志）
      - 所有写操作通过 ActionService 执行（不直接调用 StateMachine）
      - _需求：9.2, 9.3, 9.4, 9.5, 9.6_

    - [ ] 6.2.3 实现 HTTP 服务器（server.go）
      - 注册所有路由（使用 Go 1.22 标准库路由）
      - 注册 Jellyfin webhook 路由（POST /webhook/jellyfin）
      - 实现 GET /health 端点：返回 DB 连接状态、OneDrive 挂载状态、系统运行时间
      - 添加日志中间件（使用 log/slog 记录请求路径、耗时）
      - 添加错误恢复中间件（防止 panic 导致服务崩溃）
      - _需求：6.1, 9.1_

  - [ ] 6.3 实现 CLI 工具（cmd/taroctl/main.go）
    - 使用 cobra 构建命令行工具
    - 实现 `list` 命令：直接查询 SQLite（只读）
    - 实现 `add` 命令：调用 WebUI API `POST /entries`
    - 实现 `pending` 命令：直接查询 SQLite
    - 实现 `select` 命令：调用 WebUI API `POST /entries/{id}/select`
    - 实现 `cancel` 命令：调用 WebUI API `POST /entries/{id}/cancel`
    - 实现 `retry` 命令：调用 WebUI API `POST /entries/{id}/retry`
    - 实现 `retry --all` 命令：批量调用 retry API
    - 实现 `status` 命令：调用 WebUI API `GET /status`
    - 添加 --config 参数支持（读取 server.port 用于 API 调用）
    - _需求：10.1, 10.2, 10.3, 10.4, 10.5, 10.6_

  - [ ] 6.4 集成动作服务层到三端交互界面
    - WebUI：条目详情页"重试"/"取消"/"选择资源"按钮均调用 ActionService
    - CLI：`taroctl retry/cancel/select` 命令均通过 HTTP API 调用 ActionService
    - TG Bot：失败通知的"重试"按钮、资源选择按钮均调用 ActionService
    - 确保三端行为完全一致（同一套业务逻辑）
    - _需求：13.2, 13.3, 13.4, 13.5_

- [ ] 7. 部署配置与文档

  - [ ] 7.1 创建 taro 主服务 Dockerfile
    - 多阶段构建：builder 阶段编译 Go 二进制（CGO_ENABLED=0）
    - 最终镜像使用 alpine，复制二进制和 config.yaml.example
    - 暴露 8080 端口
    - _需求：12.1_

  - [ ] 7.2 创建 docker-compose.yml
    - 定义 taro 服务：挂载 config.yaml、data 目录、OneDrive 挂载点
    - 配置环境变量示例（敏感信息通过环境变量传入）
    - 配置重启策略（unless-stopped）
    - 配置日志轮转（json-file driver，max-size: 10m，max-file: 3）
    - 配置 healthcheck（调用 GET /health，间隔 30s）
    - _需求：12.2_

  - [ ] 7.3 编写 README.md
    - 项目简介和架构图
    - 部署步骤（树莓派 Docker、HuggingFace Space）
    - 配置文件说明（config.yaml 各字段含义）
    - OAuth2 认证配置指南（Bangumi、Trakt）
    - Jellyfin webhook 插件配置说明（自定义 JSON 模板）
    - CLI 使用示例
    - 常见问题排查（OneDrive 挂载、PikPak 空间不足、Prowlarr 索引器配置）

- [ ] 8. Final Checkpoint - 完整系统测试
  - 在树莓派环境部署完整系统
  - 验证所有 15 个需求的端到端流程
  - 压力测试：同时处理 10 个条目，监控资源占用
  - 验证系统重启后的恢复逻辑
  - 询问用户是否有问题

## 注意事项

1. 所有数据库操作必须使用事务保护，确保状态一致性
2. 所有 goroutine 必须使用 defer recover() 防止 panic
3. 所有外部 API 调用必须设置超时（默认 30 秒）
4. 所有敏感信息（token、密码）不得记录到日志
5. 编译时使用 `CGO_ENABLED=0` 确保静态链接，适配树莓派交叉编译
6. Bangumi 条目标题优先使用日文原名（name 字段）而非中文译名（name_cn）
7. 分辨率优先级：1080p > 1080i > 720p > 480p > other
8. manual 来源的 source_id 使用 UUID
9. Token 刷新需要互斥锁保护配置文件写入；写回失败只告警不阻断主流程
10. 调度器需要信号量限制并发数，避免对外部 API 造成压力
11. PikPak 登录态过期需要自动重新登录
12. Prowlarr 不可达时条目保持 pending（retryable）；无结果时转为 failed（permanent）——这是关键设计点，不得随意修改
13. 数据库迁移使用简单的 CREATE TABLE IF NOT EXISTS 策略（v1 版本）
14. Bangumi uid 若配置中未提供，启动时通过 GET /v0/me 自动获取
15. 使用 log/slog 作为日志库（Go 1.21+ 标准库），日志轮转交给 Docker 管理；禁止使用 fmt.Printf 输出错误
16. 实现 GET /health 端点供 Docker healthcheck 使用
17. **职责边界**：只有 StateMachine 能改状态；WebUI/Bot/CLI 只能通过 ActionService 触发用户动作；系统自动流转由调度器直接调用业务模块
18. **失败分类**：所有 failed 转换必须通过 TransitionToFailed 指定 FailureCode；failure_kind 由 FailureKindOf(code) 自动推导，不得手动指定
19. **外部任务幂等**：PikPak 和 transfer 提交前必须检查是否已有活跃任务，避免重复创建
20. **阶段时间字段**：由 StateMachine 在状态转换时自动写入，业务模块禁止预占位；超时判断必须使用 download_started_at/transfer_started_at
21. **pikpak_file_path 语义**：只存 PikPak 内部路径，不含 `pikpak:` 前缀；transfer 服务负责添加 remote 前缀
22. **路径规范化**：target_path 统一使用 `/` 分隔符、末尾带 `/`、特殊字符替换为 `_`；Jellyfin 匹配必须 normalize 两侧路径后再做 prefix match
23. **resources 表语义**：存储所有搜索结果（含被过滤项）；eligible=1 为可选资源，eligible=0 为被过滤资源；自动选择和 UI 展示只使用 eligible=1 的资源
24. **配置惰性校验**：Telegram、Bangumi、Trakt、OneDrive 配置缺失时跳过对应模块，不阻止启动
25. **taro-transfer not_found 语义**：GET /transfer/{id}/status 返回 not_found 时，主服务视为任务丢失，重新提交
