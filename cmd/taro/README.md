# Taro 主服务

## 概述

taro 主服务是媒体库自动化系统的核心组件，负责协调所有业务模块的运行。

## 功能

- 配置加载与日志初始化
- 数据库连接与自动迁移
- 状态机初始化与启动恢复
- 平台轮询（Bangumi、Trakt）
- 资源搜索（Prowlarr）
- 下载管理（PikPak）
- 转存协调（taro-transfer）
- Webhook 处理（Jellyfin）
- 平台回调（Bangumi、Trakt）
- 通知服务（Telegram）
- OneDrive 挂载健康检测
- PikPak 垃圾回收与数据清理
- 优雅关闭

## 运行

### 前置条件

1. 安装 `pikpaktui` CLI：
   ```bash
   # 从 https://github.com/Bengerthelorf/pikpaktui 下载并安装
   ```

2. 安装 `rclone`（用于 OneDrive 健康检测）：
   ```bash
   # 从 https://rclone.org/downloads/ 下载并安装
   ```

3. 准备配置文件：
   ```bash
   cp config.yaml.example config.yaml
   # 编辑 config.yaml，填写必填配置项
   ```

### 必填配置项

以下配置项必须填写，否则服务无法启动：

- `server.port`：HTTP 服务端口
- `server.db_path`：SQLite 数据库路径
- `prowlarr.url`：Prowlarr API 地址
- `prowlarr.api_key`：Prowlarr API 密钥
- `pikpak.username`：PikPak 用户名
- `pikpak.password`：PikPak 密码
- `transfer.url`：taro-transfer 服务地址
- `transfer.token`：taro-transfer 认证令牌

### 可选配置项

以下配置项为可选，缺失时对应模块将被跳过：

- `bangumi.access_token`：Bangumi OAuth2 访问令牌（跳过 Bangumi 轮询）
- `trakt.client_id` 和 `trakt.access_token`：Trakt OAuth2 配置（跳过 Trakt 轮询）
- `telegram.bot_token` 和 `telegram.chat_id`：Telegram Bot 配置（跳过通知）
- `onedrive.mount_path`：OneDrive 挂载路径（跳过健康检测）

### 启动服务

```bash
# 使用默认配置文件（config.yaml）
go run main.go

# 指定配置文件路径
go run main.go -config /path/to/config.yaml

# 编译后运行
go build -o taro main.go
./taro -config config.yaml
```

### 优雅关闭

服务支持优雅关闭，接收到 SIGINT 或 SIGTERM 信号时会：

1. 停止接收新任务
2. 停止所有后台服务（调度器、轮询器、健康检测、垃圾回收）
3. 等待所有 goroutine 完成（最多 30 秒）
4. 关闭数据库连接

```bash
# 发送 SIGINT 信号（Ctrl+C）
kill -INT <pid>

# 发送 SIGTERM 信号
kill -TERM <pid>
```

## 日志

日志级别和格式可通过配置文件控制：

```yaml
logging:
  level: "info"        # debug | info | warn | error
  format: "text"       # text | json
```

- `level`：日志级别，默认 `info`
- `format`：日志格式，`text` 为人类可读格式，`json` 为结构化 JSON 格式

## 启动恢复

服务启动时会自动执行恢复逻辑：

1. 将所有 `searching` 状态的条目重置为 `pending`
2. 恢复 `downloading` 状态的条目到 PikPak 轮询队列
3. 恢复 `transferring` 状态的条目到转存轮询队列

这确保了服务重启后能够继续处理未完成的任务。

## 故障排查

### 服务无法启动

1. 检查配置文件是否存在且格式正确
2. 检查必填配置项是否已填写
3. 检查数据库路径是否可写
4. 检查 `pikpaktui` 和 `rclone` 是否已安装并在 PATH 中

### 模块被跳过

如果看到类似 "bangumi not configured, skipping bangumi poller" 的日志，说明对应模块的配置项缺失，该模块将被跳过。这是正常行为，不影响其他模块的运行。

### 日志级别调整

如果需要更详细的日志输出，可以将 `logging.level` 设置为 `debug`：

```yaml
logging:
  level: "debug"
```

## 下一步

- Task 6.1：实现 Telegram Bot 交互模块
- Task 6.2：实现 WebUI 模板和路由
- Task 6.3：实现 CLI 工具（taroctl）
