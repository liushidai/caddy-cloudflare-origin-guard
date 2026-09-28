# caddy-cloudflare-origin-guard

一个面向 Caddy v2 的 Cloudflare 源站访问防护插件。它定期读取 Cloudflare 官方公布的 IPv4/IPv6 CDN 网段，在 Caddy 中同时提供：

- 当前 Caddy 应用模块：`cloudflare_origin`；
- HTTP matcher：`cloudflare_origin`（模块 ID：`http.matchers.cloudflare_origin`）；
- Caddy `trusted_proxies` 使用的 IP source：`cloudflare`（模块 ID：`http.ip_sources.cloudflare`）。

它的防护目标是：在源站 HTTP/HTTPS 端口前，通过 matcher 和明确的拒绝规则阻止直接来自非 Cloudflare 官方 CDN 网段的请求，并让经过 Cloudflare 代理的请求能够由 Caddy 正确恢复客户端 IP。插件不代理流量，也不改变 DNS、证书或 Cloudflare 账号配置。

## 安装与构建

使用 [xcaddy](https://github.com/caddyserver/xcaddy) 将插件编译进 Caddy。当前依赖 Caddy v2.11.4：

```bash
xcaddy build v2.11.4 \
  --with github.com/liushidai/caddy-cloudflare-origin-guard=.
```

上述命令在插件源码目录执行，与仓库 `Dockerfile` 中编译本地 checkout 的方式一致。生产构建应先取得并审核完整的源码提交；例如在独立的构建目录中执行：

```bash
SOURCE_COMMIT="<经审核的40位完整提交哈希>"
git clone https://github.com/liushidai/caddy-cloudflare-origin-guard.git source-checkout
cd source-checkout
git checkout --detach "$SOURCE_COMMIT"
xcaddy build v2.11.4 --with github.com/liushidai/caddy-cloudflare-origin-guard=.
```

将生成的 Caddy 替换为实际运行的二进制，并用 `caddy list-modules` 检查是否包含上述三个模块。Docker 构建同样编译本地 checkout，需先检出审核过的提交再构建镜像。固定源码提交不等于固定构建镜像的 tag/摘要，也不保证逐字节可复现；生产镜像可进一步固定经审核的 digest。

## Docker 部署

仓库根目录提供可选的 Docker Compose 部署示例。先复制并编辑脱敏示例，再启动服务：

```bash
cp examples/Caddyfile.docker.example Caddyfile
# 编辑 Caddyfile：替换示例域名，并将占位 respond 改为实际站点配置。
docker compose up -d --build
```

也可以只构建镜像：

```bash
docker build -t caddy-cloudflare-origin-guard .
```

`compose.yaml` 只是演示配置，不可直接替代生产部署。它由 Compose 管理单个 `caddy_bridge` 双栈 bridge 网络（`enable_ipv6: true`），明确发布 `80:80/tcp`、`443:443/tcp`、`443:443/udp`（HTTP/3），将根目录 `./Caddyfile` 只读挂载到 `/etc/caddy/Caddyfile`，并通过命名卷持久化 `/data` 和 `/config`。实际网络名通常带 Compose 项目名前缀，并不需要固定网络名、IPAM 地址或机器路径。`/data` 中包含证书、插件 LKG（Last Known Good）地址缓存等运行数据，删除或不持久化它会丢失这些状态。

若同一个 Compose 项目中已有 `photo` 服务，给**现有服务**补充网络配置即可；此片段不创建服务或指定镜像：

```yaml
services:
  photo:
    networks:
      - caddy_bridge
```

`caddy` 与 `photo` 同处该网络时，HTTPS 站点才可使用 `reverse_proxy photo:8080` 解析并访问它；仅发布宿主端口不提供容器间的服务名解析。容器运行时必须能出站访问 Cloudflare 官方 IPv4、IPv6 网段端点；首次启动和后续更新均依赖该访问，网络不可用时仅能在有效 LKG 存在且未超过 `max_stale` 时初始化。

已有 Compose 网络从 IPv4-only 改为双栈时需要重建网络并重建相关容器，可能造成停机；预先备份 `/data`，保持原 Compose 项目名和命名卷，安排维护窗口，不要运行 `docker compose down -v`。`enable_ipv6` 仅保证网络分配 IPv6 地址，不保证所有 Docker 网关模式和平台都使用 IPv6 DNAT；上线前必须核对所用网络配置、IPv6 TCP/UDP 转发路径，并从真正的外部 IPv6 客户端发起请求检查 Caddy 日志的 `request.remote_ip`。在 IPv4-only bridge 上发布宿主 IPv6 端口时，`docker-proxy` 可能把真实 IPv6 来源改写为网桥地址；插件只能判断 Caddy 实际收到的 `RemoteAddr`，无法还原上游连接的原始 peer。绝不可通过将 `172.16.0.0/12` 加入可信代理或允许来源来规避此问题。localhost 转发、Docker Desktop、rootless Docker、其他 userland proxy、负载均衡器等也可能改写 peer。Linux 单机入口可按特殊拓扑选择 host network；该模式不能与 `ports` 同时使用，不是 Compose 默认配置。若入口接收 PROXY protocol，也不得在公网入口无条件启用，应仅在已验证且受信任的前置代理链路中使用。

## 使用教程

以下流程适用于新部署，也可用于将已有 Caddy 站点迁移为仅允许 Cloudflare 回源。

1. 准备 DNS 与网络：将需要保护的域名指向源站，并在 Cloudflare 启用代理；确保源站的 `80/tcp`、`443/tcp`（如使用 HTTP/3，还需 `443/udp`）可按预期访问。普通直连站点不应使用下方 matcher。
2. 准备配置：复制 `examples/Caddyfile.docker.example` 为根目录 `Caddyfile`，将示例域名和 HTTPS 站点的占位响应替换为实际站点与 `reverse_proxy`。HTTP 和 HTTPS 都要在 `route` 中先拒绝非 Cloudflare 来源；HTTP 站点仅在守卫通过后才重定向：

   ```caddyfile
   route {
       @not_cloudflare not cloudflare_origin
       respond @not_cloudflare "Forbidden" 403
       # 此处放站点的后续处理（HTTP 重定向或 HTTPS 业务处理）。
   }
   ```

3. 构建并启动：在仓库根目录执行：

   ```bash
   docker compose up -d --build
   ```

4. 确认模块和服务状态：

   ```bash
   docker compose exec caddy caddy list-modules | grep cloudflare
   docker compose ps
   docker compose logs caddy
   ```

   输出应包含 `cloudflare_origin`、`http.ip_sources.cloudflare` 和 `http.matchers.cloudflare_origin`。首次加载必须成功获取官方网段，或使用未过期的 LKG 缓存；两者均不可用时 Caddy 会拒绝加载该配置。

5. 验证防护行为：通过 Cloudflare 代理访问受保护域名应获得正常业务响应；真正外部客户端直接访问源站的 HTTP、HTTPS，或伪造 `CF-Connecting-IP`、`X-Forwarded-For`，均应命中 `403`。IPv4、IPv6、HTTP/3 的具体检查命令见下节。

6. 验证真实连接来源：通过 Cloudflare 的请求可由 Caddy `trusted_proxies` 处理 `CF-Connecting-IP`；直连请求的伪造 Header 不应影响 matcher。检查 JSON 访问日志的 `request.remote_ip` 是否为真正外部连接的 IP；`request.client_ip` 可能由代理头推导，不能作为 peer 的证据。

## 上线前连接与协议验证

示例域名 `protected.example.test` 和源站地址 `192.0.2.10`、`2001:db8::10` 均为文档占位符，执行前换成真实域名和真实源站地址；以下直连命令必须在**真正外部**的 IPv4/IPv6 客户端执行，不要在宿主机或容器本地回环测试。HTTPS 证书应被测试客户端信任，必要时配置 `--cacert`，不要把 TLS 错误误判为来源守卫结果。`--resolve` 同时保留请求 Host 和 TLS SNI，`--noproxy '*'` 禁止环境代理干扰：

```bash
curl --noproxy '*' -4 -i --resolve protected.example.test:80:192.0.2.10 http://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:80:[2001:db8::10]' http://protected.example.test/
curl --noproxy '*' -4 -i --resolve protected.example.test:443:192.0.2.10 https://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:443:[2001:db8::10]' https://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:443:[2001:db8::10]' \
  -H 'CF-Connecting-IP: 192.0.2.10' -H 'X-Forwarded-For: 192.0.2.10' https://protected.example.test/
```

上述直连均应返回 `403`，特别是带伪造头的请求。检查 `docker compose logs caddy` 中对应 JSON access log 的 `request.remote_ip`：它必须是外部发起请求的 IP，而不是 Docker 网桥地址；`request.client_ip`（可能来自 `CF-Connecting-IP`）不是 peer 证据。Cloudflare 入口访问 HTTPS 应返回正常业务响应；Cloudflare 边缘可能自行重定向 HTTP，因此边缘 HTTP 的响应不能证明源站 HTTP 守卫已测试。访问橙云域名时 `curl -4`/`curl -6` 只选择客户端到 Cloudflare 边缘的地址族，不能据此断言边缘到源站使用的地址族。

检查宿主 IPv6 NAT：按实际防火墙后端选择只读命令 `sudo ip6tables -t nat -S DOCKER` 或 `sudo nft list ruleset`，核对 IPv6 的 `80/tcp`、`443/tcp`、`443/udp` 发布与转发/NAT 路径；并非所有 Docker 网关模式都使用 DNAT。规则列表不是流量真实到达及来源未被改写的充分证据，还要结合外部 IPv6 请求、必要时的宿主抓包和上述访问日志验证。HTTP/3 另需先用 `curl -V` 确认客户端支持 HTTP3，然后在外部客户端强制 QUIC：

```bash
curl --noproxy '*' -4 --http3-only -i --resolve protected.example.test:443:192.0.2.10 https://protected.example.test/
curl --noproxy '*' -6 --http3-only -i --resolve 'protected.example.test:443:[2001:db8::10]' https://protected.example.test/
```

直连应均返回 `403`，并在对应 JSON 日志中确认 `request.proto` 为 `HTTP/3.0`（或实际 Caddy 输出的 HTTP/3 协议标记）；`--http3-only` 不允许静默回退到 TCP。经橙云访问 HTTP/3 只证明客户端到边缘的 QUIC 链路，不证明边缘到源站使用 HTTP/3 或 IPv6。

## 实测结果

在此前公开网络、Docker 化 Caddy 与 Cloudflare 代理的端到端验证中，已确认以下行为（仅限当时拓扑和已测协议，不能视作本次双栈网络与 HTTP/3 的复测）：

- 自定义 Caddy v2.11.4 构建可加载 app、IP source 与 matcher 三个模块。
- IPv4 和 IPv6 的 Cloudflare 代理请求均通过 matcher，并由 Caddy 恢复真实客户端 IP。
- IPv4 和 IPv6 的直连请求均被拒绝；即使附加伪造的 `CF-Connecting-IP` 和 `X-Forwarded-For` 也无法绕过。
- 直接连接源站但携带受保护域名的 Host/SNI 仍会被拒绝。
- 连续执行多次 Caddy reload 后，允许/拒绝语义保持不变。
- 首次网络加载成功后，LKG 缓存已生成并以 `0600` 权限保存。

此前也观察到自动 HTTP 到 HTTPS 跳转可能在 HTTPS 站点的 matcher 之前处理 HTTP 请求。上面的 `auto_https disable_redirects` 与显式受保护 HTTP 站点是对此行为的配置修复方法，不代表已经对本次配置完成复测。实际部署仍须按上节验证 HTTP、HTTPS、HTTP/3（如启用）、`RemoteAddr`、Cloudflare 回源模式与前置代理链路。

## Caddyfile 配置

完整示例：

```caddyfile
{
	auto_https disable_redirects

	# 可选。省略时使用默认值：
	# refresh_interval 1h、timeout 10s、max_stale 720h（30 天）。
	cloudflare_origin {
		refresh_interval 1h
		timeout 10s
		max_stale 720h
	}

	servers {
		trusted_proxies cloudflare
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP
	}
}

http://protected.example.test {
	log {
		output stdout
		format json
	}

	route {
		@not_cloudflare not cloudflare_origin
		respond @not_cloudflare "Forbidden" 403
		redir https://protected.example.test{uri} 308
	}
}

https://protected.example.test {
	log {
		output stdout
		format json
	}

	route {
		@not_cloudflare not cloudflare_origin
		respond @not_cloudflare "Forbidden" 403
		# 将下一行替换为 reverse_proxy photo:8080（两者须位于同一 Compose 网络）。
		respond "受保护站点占位响应" 200
	}
}
```

`route` 固定拒绝规则先于 HTTP 重定向或 HTTPS 业务处理，避免默认指令排序使重定向绕过守卫。Caddy 自动生成的 HTTP 到 HTTPS 重定向不经过 HTTPS 站点 matcher；若 HTTP 也必须只响应 Cloudflare，应全局 `auto_https disable_redirects` 并显式配置受保护的 `http://` 站点。该选项会关闭**本配置中其他普通站点**的自动 HTTP 到 HTTPS 重定向，需为它们单独配置所需的 HTTP 行为；它不表示插件会拦截 ACME 挑战或所有 80 端口响应。按域名声明的站点也不约束未匹配的 Host。自动证书管理仍可能使用 HTTP-01 或 TLS-ALPN-01 挑战，应按实际证书方案确认验证流量可到达，必要时使用 DNS-01。上述 `log` 将各站点 JSON 访问日志输出到 stdout，便于检查 `request.remote_ip`。

`cloudflare_origin` 全局块是可选的；可配置项只有：

- `refresh_interval`：周期刷新间隔，默认 `1h`；
- `timeout`：每次抓取请求的超时，默认 `10s`；
- `max_stale`：初始化时网络不可用且需要回退本地 LKG（Last Known Good）缓存时允许的最长年龄，默认 `720h`（30 天）；不限制已发布的运行内存快照存活时间。

`servers { trusted_proxies cloudflare }` 是可选的 Caddy 全局配置，但只有配置后 Caddy 才会把 Cloudflare 网段作为可信代理来源，用于处理 `X-Forwarded-For`、`X-Forwarded-Proto`、`CF-Connecting-IP` 等代理头。`trusted_proxies` 只负责 Caddy 的真实客户端 IP 与代理头处理，不能代替入口 ACL；来源拒绝仍必须使用 matcher 配合 `403` 规则。
不要将 Docker 网桥网段（如 `172.16.0.0/12`）加入可信代理或来源允许列表。

插件本身不把 `CF-Connecting-IP` 当作来源认证依据。matcher 只匹配 Caddy 收到的连接 `RemoteAddr`（包括 HTTP/3 的连接来源），因此应结合实际部署链路验证 Caddy 看到的 peer；已被代理改写的原始来源无法由插件还原。

## 工作方式与默认策略

- 插件从 Cloudflare 官方固定端点获取完整的 IPv4 和 IPv6 CDN ranges，并校验后发布为不可变快照；更新周期带有少量抖动。两个端点的更新是全量事务，只有 IPv4 与 IPv6 都成功获取并校验后才会发布新快照。
- 启动时优先请求网络。网络抓取或解析失败时，才回退到本地 LKG 缓存；网络成功取得的新快照会原子写入缓存。
- 默认缓存文件为 `Caddy AppDataDir/cloudflare_origin/lkg.json`，缓存文件权限为 `0600`。具体 `AppDataDir` 由 Caddy 运行环境决定。
- 初始化时只有不超过 `max_stale` 的有效缓存才会回退使用。网络和缓存都不可用、缓存损坏或已过期时，插件不会生成一个“允许全部来源”的快照，而会让配置初始化失败（fail-closed）。`max_stale` 不是运行中已发布内存快照的过期阈值。
- 周期刷新失败时保留当前已发布快照，不会因一次临时网络错误放宽防护。

matcher 只判断 Caddy 收到的连接 `RemoteAddr` 是否属于当前 Cloudflare CDN 网段快照。它不根据 Host、URL、User-Agent、TLS 指纹或请求头判断来源。插件本身只提供 matcher 和 IP source；只有将 `@not_cloudflare not cloudflare_origin` 与 `respond @not_cloudflare "Forbidden" 403`（或等效拒绝处理）一同配置，非 Cloudflare 来源才会被拒绝。

## 防护边界

本插件是 HTTP 站点级守卫，不是网络层防火墙；它用于收紧源站受保护站点的 HTTP/HTTPS（含启用时的 HTTP/3）请求。它**不**防护以下情况：

- 历史 DNS 记录、泄露过的旧 IP，或其他仍指向源站的解析记录；
- 其他域名、其他端口，或未在 Caddy 中使用 matcher 的站点；
- TCP/TLS 端口扫描、网络层攻击、资源耗尽或 DDoS；
- 绕过当前 Caddy HTTP/HTTPS 入口的访问路径。

Cloudflare WARP 流量不以 AS13335 判断。插件只使用 Cloudflare 官方发布的 CDN ranges；是否属于 WARP、ASN 或其他 Cloudflare 网络不会额外改变 matcher 结果。

## 外部增强与非目标

AOP、操作系统 firewall、防火墙规则和 Cloudflare Tunnel 可以作为额外的外部防护层，但它们不是本插件的一部分，也不由本插件配置或管理。需要更强的源站隐藏、网络层限制或 Cloudflare 边缘策略时，应单独部署相应方案。

本项目当前不提供 DNS 管理、Cloudflare API Token 管理、Tunnel、WAF、Enterprise 功能、AOP 或 OS firewall 集成，也不提供 metrics、admin API 等额外接口。

当前版本提供的是 `cloudflare_origin` matcher；不提供 `cloudflare_only` 语法糖。请使用 `@not_cloudflare not cloudflare_origin` 配合 `respond ... 403` 明确配置拒绝规则。
