# BJ 部署（M3）

权威事实见 `docs/context/infra-bj.md`。本文件只记录部署动作与回滚方式。

## 变更清单（全部在 BJ 上）

| # | 变更 | 说明 |
|---|---|---|
| 1 | `/opt/ft-server/ft-server` | Linux amd64 二进制（交叉编译，约 7 MB），回环端口 18790 |
| 2 | `/etc/systemd/system/ft-server.service` | `GOMEMLIMIT=160MiB` + `MemoryMax=256M`，`Restart=on-failure` |
| 3 | `/var/log/ft-server/` | 服务 JSONL 事件日志 |
| 4 | `/opt/1panel/www/sites/ft.cwlindean.cn/{index,log,proxy,ssl}` | 站点目录与反代片段 |
| 5 | `/opt/1panel/www/conf.d/ft.cwlindean.cn.conf` | 80 端口 ACME + 443 站点；**只 `nginx -t` + reload，不 restart** |
| 6 | 证书 `sites/ft.cwlindean.cn/ssl/{fullchain,privkey}.pem` | 由 Let's Encrypt HTTP-01 签发（域名解析在 ESA，ESA 回源 80） |
| 7 | 运维仓库回写 | `修改log.md` 与《服务器信息-BJ》 |

不开新端口：443 由现有 openresty 复用，18790 只绑 127.0.0.1。M3 不需要 UDP（打洞在 M4）。

## 执行顺序

```bash
# 1. 上传并安装
install -d /opt/ft-server /var/log/ft-server
install -m 0755 ft-server /opt/ft-server/ft-server
install -m 0644 ft-server.service /etc/systemd/system/ft-server.service
systemctl daemon-reload && systemctl enable --now ft-server
curl -s http://127.0.0.1:18790/healthz

# 2. 站点目录 + 临时自签证书（让 nginx -t 先过）
install -d /opt/1panel/www/sites/ft.cwlindean.cn/{index,log,proxy,ssl,index/.well-known/acme-challenge}
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -subj "/CN=ft.cwlindean.cn" \
  -keyout .../ssl/privkey.pem -out .../ssl/fullchain.pem

# 3. 站点配置 + 反代片段，校验并 reload（禁止 restart）
cp ft.cwlindean.cn.conf /opt/1panel/www/conf.d/
cp ft-proxy.conf /opt/1panel/www/sites/ft.cwlindean.cn/proxy/
nginx -t && nginx -s reload

# 4. 申请正式证书（HTTP-01 经 ESA 回源到 80）
certbot certonly --webroot -w /opt/1panel/www/sites/ft.cwlindean.cn/index \
  -d ft.cwlindean.cn --agree-tos -m <邮箱> --non-interactive
cp /etc/letsencrypt/live/ft.cwlindean.cn/fullchain.pem .../ssl/
cp /etc/letsencrypt/live/ft.cwlindean.cn/privkey.pem   .../ssl/
nginx -t && nginx -s reload

# 5. 自测
curl -s https://ft.cwlindean.cn/healthz
```

## 回滚

```bash
systemctl disable --now ft-server
rm -f /opt/1panel/www/conf.d/ft.cwlindean.cn.conf
rm -rf /opt/1panel/www/sites/ft.cwlindean.cn
nginx -t && nginx -s reload
rm -rf /opt/ft-server /var/log/ft-server
```

回滚只删本项目新增文件，不触碰任何既有站点配置。

## 监控与日志（M8）

- `/metrics`（loopback，经 openresty 不对外）：`version/uptime_s/peers/relay_rooms/relay_bytes/goroutines/heap_mb/sys_mb`。
- `deploy/logrotate/ft-server` 安装到 `/etc/logrotate.d/ft-server`：daily、14 天、compress、copytruncate。
- 二进制回滚前先备份：`cp -a /opt/ft-server/ft-server /opt/ft-server/ft-server.bak-$(date +%Y%m%d-%H%M)`。
