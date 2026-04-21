SELECT id, title, status FROM entries WHERE status IN ('found', 'downloading', 'downloaded') ORDER BY created_at DESC LIMIT 3;
