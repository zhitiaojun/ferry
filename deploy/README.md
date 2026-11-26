# `ft-server` 私有化部署指南

`ft-server` 是轻渡 Ferry 的信令、加密中继、临时快传门户与双端口 UDP STUN 服务端，采用单一 Go 二进制与全内存状态设计，推荐监听本机回环端口并通过 Nginx / OpenResty / Caddy 提供 HTTPS 与 WSS 反向代理。

## 部署组件清单

| 文件 | 目标位置 | 说明 |
|---|---|---|
| `ferry-server-linux-amd64` | `/opt/ft-server/ft-server` | Linux amd64 服务端可执行文件 |
| [`ft-server.service`](ft-server.service) | `/etc/systemd/system/ft-server.service` | systemd 守护单元（默认 `GOMEMLIMIT=160MiB`、`MemoryMax=256M`） |
| [`logrotate/ft-server`](logrotate/ft-server) | `/etc/logrotate.d/ft-server` | 按天轮转 JSONL 事件日志（保留 14 天并压缩） |
| [`nginx/ferry.example.conf`](nginx/ferry.example.conf) | `/etc/nginx/conf.d/ferry.conf` | Nginx HTTPS + WSS 站点配置模板 |
| [`nginx/ft-proxy.conf`](nginx/ft-proxy.conf) | 按需引入 | `/signal` 与 `/relay` WebSocket 代理片段 |

## 部署步骤

### 1. 安装二进制与 systemd 服务

```bash
install -d /opt/ft-server /var/log/ft-server
install -m 0755 ferry-server-linux-amd64 /opt/ft-server/ft-server
install -m 0644 deploy/ft-server.service /etc/systemd/system/ft-server.service
install -m 0644 deploy/logrotate/ft-server /etc/logrotate.d/ft-server

# 可选：若需要配置服务端门户解锁口令，取消下行注释并写入口令
# echo 'FT_WEB_PASSWORD=your_strong_password' > /opt/ft-server/web.env
# chmod 0600 /opt/ft-server/web.env

systemctl daemon-reload
systemctl enable --now ft-server
curl -fsS http://127.0.0.1:18790/healthz
```

### 2. 配置反向代理与 TLS 证书

1. 将 [`nginx/ferry.example.conf`](nginx/ferry.example.conf) 中的 `ferry.example.com` 与证书路径替换为你的域名和证书路径。
2. 确保 `/signal`、`/relay` 与 `/web/` 路由均启用了 `Upgrade` / `Connection "upgrade"`、关闭 `proxy_buffering off;` 并设置较长的 `proxy_read_timeout`（建议 `3600s`）。
3. 若域名经由 **阿里云 ESA** 等 CDN/边缘安全加速网关接入，请在控制台开启 WebSocket 支持，并注意 **UDP STUN 打洞端口需直连服务器公网 IP**（不经过 HTTP CDN）。
4. 校验并平滑重载配置：

```bash
nginx -t && nginx -s reload
```

### 3. 防火墙放行 UDP STUN 端口

`deploy/ft-server.service` 默认启用双端口 STUN（`0.0.0.0:40000,0.0.0.0:40001`），请在云安全组及本机防火墙放行 `40000-40001/udp`。

### 4. 健康检查与指标观测

- **存活检查**：`https://ferry.example.com/healthz`（返回 HTTP 200）。
- **运行指标**：`http://127.0.0.1:18790/metrics`（JSON 格式输出 `version`、`uptime_s`、`peers`、`relay_rooms`、`relay_bytes`、`goroutines`、`heap_mb`、`sys_mb`；建议仅限本机或内网访问）。
