-- 媒体条目主表
CREATE TABLE IF NOT EXISTS entries (
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
CREATE TABLE IF NOT EXISTS resources (
    id          TEXT PRIMARY KEY,              -- UUID
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    title       TEXT NOT NULL,                 -- 资源文件名
    magnet      TEXT NOT NULL,                 -- 磁力链接
    size        INTEGER,                       -- 文件大小（字节）
    seeders     INTEGER,                       -- 做种数
    resolution  TEXT,                          -- 解析出的分辨率（'1080p'|'1080i'|'720p'|'480p'|'other'）
    indexer     TEXT,                          -- 来源索引器名称
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 状态变更审计日志表
CREATE TABLE IF NOT EXISTS state_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id    TEXT NOT NULL REFERENCES entries(id),
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    reason      TEXT,                          -- 变更原因（可选）
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_entries_status ON entries(status);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source, source_id);
CREATE INDEX IF NOT EXISTS idx_resources_entry ON resources(entry_id);
CREATE INDEX IF NOT EXISTS idx_state_logs_entry ON state_logs(entry_id);
CREATE INDEX IF NOT EXISTS idx_entries_failed_at ON entries(failed_at) WHERE status = 'failed';
