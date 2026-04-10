# Taro - 个人媒体库自动化系统

[![CI](https://github.com/michealmachine/taro/actions/workflows/ci.yml/badge.svg)](https://github.com/michealmachine/taro/actions/workflows/ci.yml)
[![Release](https://github.com/michealmachine/taro/actions/workflows/release.yml/badge.svg)](https://github.com/michealmachine/taro/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/michealmachine/taro)](https://go.dev/)
[![License](https://img.shields.io/github/license/michealmachine/taro)](LICENSE)

Taro 是一个运行在树莓派上的个人媒体库自动化管理系统，将"想看"的意图转化为"已入库"的结果，全程自动化。

## ✨ 特性

- 🎬 **自动化流程**：从收藏到入库，全程自动化
- 📱 **多端交互**：WebUI、CLI、Telegram Bot 三种交互方式
- 🔄 **智能状态机**：11 种状态精确追踪每个条目的生命周期
- 🌐 **平台集成**：支持 Bangumi（动漫）和 Trakt（电影/剧集）
- 💾 **离线下载**：通过 PikPak 云端离线下载
- ☁️ **云端转存**：利用 HuggingFace Space 转存到 OneDrive
- 🎯 **智能重试**：失败后智能选择重试起点，避免重复下载
- 🔍 **资源搜索**：通过 Prowlarr 聚合多个资源站点
- 📊 **状态追踪**：完整的审计日志和状态历史

## 🏗️ 架构

```
用户收藏 (Bangumi/Trakt)
    ↓
资源搜索 (Prowlarr)
    ↓
离线下载 (PikPak)
    ↓
云端转存 (taro-transfer on HuggingFace Space)
    ↓
本地挂载 (OneDrive on Raspberry Pi)
    ↓
媒体入库 (Jellyfin)
    ↓
状态回调 (更新平台状态)
```

## 🚀 快速开始

### 前置要求

- 树莓派（推荐 4B 或更高）
- Docker 和 Docker Compose
- OneDrive 账号（通过 rclone 挂载）
- PikPak 账号
- Prowlarr 实例
- Jellyfin 实例
- Telegram Bot Token（可选）
- Bangumi/Trakt OAuth2 凭证

### 使用 Docker Compose 部署

1. 下载配置文件模板：

```bash
wget https://raw.githubusercontent.com/michealmachine/taro/master/config.yaml.example -O config.yaml
```

2. 编辑 `config.yaml`，填入你的配置

3. 创建 `docker-compose.yml`：

```yaml
version: '3.8'
services:
  taro:
    image: ghcr.io/michealmachine/taro:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - ./data:/data
      - /mnt/onedrive:/mnt/onedrive:ro
    environment:
      - TARO_PIKPAK_PASSWORD=${PIKPAK_PASSWORD}
      - TARO_TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
      - TARO_TRANSFER_TOKEN=${TRANSFER_TOKEN}
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

4. 启动服务：

```bash
docker-compose up -d
```

### 使用二进制文件部署

1. 从 [Releases](https://github.com/michealmachine/taro/releases) 下载对应架构的二进制文件

2. 解压并运行：

```bash
tar xzf taro-v1.0.0-linux-arm64.tar.gz
./taro-linux-arm64 --config config.yaml
```

## 📖 文档

完整的项目文档位于 `.kiro/specs/taro/` 目录：

- [需求文档](/.kiro/specs/taro/requirements.md) - 15 个需求及验收标准
- [设计文档](/.kiro/specs/taro/design.md) - 架构设计、数据库设计、模块设计
- [任务文档](/.kiro/specs/taro/tasks.md) - 8 阶段实现计划

## 🛠️ 开发

### 环境要求

- Go 1.22+
- Docker（用于构建镜像）

### 本地开发

```bash
# 克隆仓库
git clone https://github.com/michealmachine/taro.git
cd taro

# 安装依赖
go mod download

# 运行测试
go test ./...

# 构建
go build -o taro ./cmd/taro
go build -o taroctl ./cmd/taroctl

# 运行
./taro --config config.yaml
```

### CLI 工具

```bash
# 列出所有条目
taroctl list

# 添加条目
taroctl add "进击的巨人"

# 查看待选择队列
taroctl pending

# 重试失败条目
taroctl retry <entry_id>

# 查看系统状态
taroctl status
```

## 🤝 贡献

欢迎贡献！请查看 [任务文档](/.kiro/specs/taro/tasks.md) 了解当前的开发进度。

## 📝 许可证

[MIT License](LICENSE)

## 🙏 致谢

- [Prowlarr](https://github.com/Prowlarr/Prowlarr) - 资源索引聚合
- [Jellyfin](https://github.com/jellyfin/jellyfin) - 媒体服务器
- [PikPak](https://mypikpak.com/) - 云存储服务
- [Bangumi](https://bgm.tv/) - 动漫追踪平台
- [Trakt](https://trakt.tv/) - 影视追踪平台

## 📊 项目状态

当前版本：**开发中**

查看 [任务文档](/.kiro/specs/taro/tasks.md) 了解详细的开发进度。

---

**注意**：这是一个个人项目，主要用于学习和个人使用。如果你觉得有用，欢迎 Star ⭐
