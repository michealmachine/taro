# taro

个人媒体库自动化系统，运行在树莓派上。将"想看"的意图转化为"已入库"的结果，全程自动化。

[![CI](https://github.com/michealmachine/taro/actions/workflows/ci.yml/badge.svg)](https://github.com/michealmachine/taro/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/docker/v/double2and9/taro?label=docker)](https://hub.docker.com/r/double2and9/taro)
[![Go Version](https://img.shields.io/github/go-mod/go-version/michealmachine/taro)](https://go.dev/)

## 工作流程

```
Bangumi / Trakt 收藏
        ↓
  Prowlarr 资源搜索
        ↓
   PikPak 离线下载
        ↓
 taro-transfer 转存
 (HuggingFace Space)
        ↓
  OneDrive → 本地挂载
        ↓
    Jellyfin 入库
        ↓
  平台状态回调
```

## 开发进度

| 阶段 | 状态 | 说明 |
|------|------|------|
| 基础设施（DB、状态机、配置） | ✅ 完成 | |
| 核心业务模块 | ✅ 完成 | 搜索、下载、转存、Webhook、平台回调 |
| taro-transfer 子服务 | ✅ 完成 | 部署在 HuggingFace Space |
| 调度器与主服务集成 | ✅ 完成 | |
| 单元测试 | ✅ 完成 | 状态机、去重、Webhook 匹配、智能重试、GC |
| Checkpoint - 核心流程验证 | 🔄 进行中 | 端到端测试中 |
| WebUI | ⏳ 待开发 | templ + HTMX |
| Telegram Bot | ⏳ 待开发 | |
| CLI (taroctl) | ⏳ 待开发 | |

## 部署

### 前置要求

- 树莓派（4B 或更高）+ Docker
- PikPak 账号
- Prowlarr 实例
- OneDrive（通过 rclone 挂载）
- Jellyfin 实例
- HuggingFace 账号（用于部署 taro-transfer）

### taro-transfer

taro-transfer 是一个独立的转存服务，负责将文件从 PikPak 复制到 OneDrive。需要部署在有公网访问的环境（推荐 HuggingFace Space）。

1. Fork 本仓库，在 HuggingFace 创建一个 Docker Space
2. 配置以下 Secrets：
   - `RCLONE_CONFIG_B64`：base64 编码的 rclone.conf（需包含 pikpak 和 onedrive 两个 remote）
   - `TARO_TRANSFER_TOKEN`：自定义的访问令牌

### taro 主服务

1. 创建 `config.yaml`（参考 `config.yaml.example`）：

```yaml
server:
  port: 8080
  db_path: /data/taro.db

prowlarr:
  url: "http://your-prowlarr:9696"
  api_key: "your-api-key"

pikpak:
  username: "your@email.com"
  password: "your-password"

transfer:
  url: "https://your-space.hf.space"
  token: "your-token"

onedrive:
  media_root: "Media"        # OneDrive 中的媒体根目录
  mount_path: "/mnt/onedrive" # 本地挂载路径（留空则跳过健康检测）
```

2. 创建 `docker-compose.yml`：

```yaml
services:
  taro:
    image: double2and9/taro:latest
    restart: unless-stopped
    ports:
      - "8090:8080"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - ./data:/data
      - ./data/pikpaktui:/root/.config/pikpaktui
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 3s
      retries: 3
```

3. 启动：

```bash
mkdir -p data
docker-compose up -d
```

> PikPak 登录凭据从 config.yaml 读取，容器启动时自动登录，无需手动操作。

### Jellyfin Webhook 配置

安装 Jellyfin Webhook 插件，添加一个通用 Webhook，URL 设为：

```
http://your-taro-host:8090/webhook/jellyfin
```

请求体模板（JSON）：

```json
{
  "NotificationType": "{{NotificationType}}",
  "ItemType": "{{ItemType}}",
  "Path": "{{Path}}"
}
```

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /health | 健康检查 |
| POST | /entries | 手动添加条目 |
| POST | /webhook/jellyfin | Jellyfin 入库回调 |

**POST /entries 示例：**

```bash
curl -X POST http://localhost:8090/entries \
  -H 'Content-Type: application/json' \
  -d '{"title":"葬送的芙莉莲","media_type":"anime","season":1}'
```

## 条目状态流转

```
pending → searching → found → downloading → downloaded → transferring → transferred → in_library
                    ↘ needs_selection ↗
任意状态 → failed / cancelled
```

## 本地开发

```bash
go mod download
go test ./...
go build -o taro ./cmd/taro
./taro --config config.yaml
```

Docker 镜像构建（手动触发 GitHub Actions）：

```bash
gh workflow run taro.yml
```

## 依赖

- [pikpaktui](https://github.com/Bengerthelorf/pikpaktui) - PikPak CLI
- [Prowlarr](https://github.com/Prowlarr/Prowlarr) - 资源索引聚合
- [rclone](https://rclone.org/) - 云存储同步
- [Jellyfin](https://github.com/jellyfin/jellyfin) - 媒体服务器
- [Bangumi](https://bgm.tv/) / [Trakt](https://trakt.tv/) - 追踪平台
