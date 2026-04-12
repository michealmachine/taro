# taro-transfer

转存服务，负责将文件从 PikPak 转存到 OneDrive。

## 功能

- 接收转存任务请求
- 使用 rclone 执行 PikPak → OneDrive 转存
- 转存成功后自动删除 PikPak 源文件
- 提供任务状态查询接口
- 内存存储任务状态（HF Space 重启后主服务会重新提交）

## API 接口

### POST /transfer

创建转存任务

**请求体：**
```json
{
  "source_path": "/downloads/进撃の巨人/episode01.mkv",
  "target_path": "/media/anime/进撃の巨人/Season 01/"
}
```

**响应：**
```json
{
  "task_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### GET /transfer/{id}/status

查询任务状态

**响应：**
```json
{
  "status": "pending|running|done|failed|not_found",
  "error": "错误信息（仅 failed 时）"
}
```

**状态说明：**
- `pending`: 任务已创建，等待执行
- `running`: 正在转存
- `done`: 转存完成
- `failed`: 转存失败
- `not_found`: 任务不存在（HF Space 重启后任务丢失）

### GET /health

健康检查

**响应：**
```json
{
  "status": "ok"
}
```

## 部署

### HuggingFace Space

1. 创建 Space，选择 Docker 运行时
2. 上传 Dockerfile 和源代码
3. 配置 rclone.conf（包含 PikPak 和 OneDrive 配置）
4. 设置环境变量（必填）：
   - `TARO_TRANSFER_TOKEN`: API 认证 token（与主服务 `transfer.token` 配置一致）

### 本地测试

```bash
# 构建
docker build -t taro-transfer .

# 运行（需要挂载 rclone.conf）
docker run -p 7860:7860 \
  -e TARO_TRANSFER_TOKEN=your_shared_token \
  -v ~/.config/rclone:/root/.config/rclone:ro \
  taro-transfer
```

## rclone 配置

需要在 `~/.config/rclone/rclone.conf` 中配置 PikPak 和 OneDrive：

```ini
[pikpak]
type = pikpak
user = your@email.com
pass = <encrypted_password>

[onedrive]
type = onedrive
token = {"access_token":"...","token_type":"Bearer",...}
drive_id = <your_drive_id>
drive_type = personal
```

## 注意事项

1. 任务状态存储在内存中，HF Space 重启后会丢失
2. 主服务会通过 `not_found` 状态检测任务丢失并重新提交
3. 转存失败不会自动重试，由主服务决定是否重试
4. 删除源文件失败不影响任务状态（标记为 done）
