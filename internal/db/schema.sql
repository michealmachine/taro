-- 媒体条目主表
CREATE TABLE IF NOT EXISTS entries (
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
CREATE TABLE IF NOT EXISTS resources (
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
CREATE TABLE IF NOT EXISTS state_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT,                          -- 变更原因（可选）
    metadata    TEXT,                          -- JSON 格式元信息（v2 候选：记录 resource_id、token_refreshed 等）
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_entries_status ON entries(status);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source, source_id);
CREATE INDEX IF NOT EXISTS idx_entries_failure_kind ON entries(failure_kind) WHERE status = 'failed';
CREATE INDEX IF NOT EXISTS idx_resources_entry ON resources(entry_id);
CREATE INDEX IF NOT EXISTS idx_resources_eligible ON resources(entry_id, eligible); -- 快速查询可选资源
CREATE INDEX IF NOT EXISTS idx_state_logs_entry ON state_logs(entry_id);
CREATE INDEX IF NOT EXISTS idx_entries_failed_at ON entries(failed_at) WHERE status = 'failed';
