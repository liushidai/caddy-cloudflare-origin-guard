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
  --with github.com/liushidai/caddy-cloudflare-origin-guard
```

也可以在项目目录中直接构建：

```bash
xcaddy build v2.11.4 --with .
```

将生成的 Caddy 替换为实际运行的二进制，并用 `caddy list-modules` 检查是否包含上述模块。

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

`compose.yaml` 默认使用 Docker bridge 网络并发布 `80` 和 `443`，将根目录 `./Caddyfile` 只读挂载到 `/etc/caddy/Caddyfile`，并通过命名卷持久化 `/data` 和 `/config`。`/data` 中包含证书、插件 LKG（Last Known Good）缓存等运行数据，删除或不持久化它会丢失这些状态。

容器运行时必须能出站访问 Cloudflare 官方 IPv4、IPv6 网段端点；首次启动和后续更新均依赖该访问，网络不可用时仅能在有效 LKG 存在且未超过 `max_stale` 时继续运行。

标准 Linux bridge 部署通常能保留进入 Caddy 的实际 TCP `RemoteAddr`，但上线前应验证 matcher 看到的 peer。localhost 转发、Docker Desktop、rootless Docker、userland proxy、负载均衡器或其他前置代理都可能改变该 peer。Linux 单机入口可按特殊拓扑选择 host network；该模式不能与 `ports` 同时使用，不是 Compose 默认配置。若入口接收 PROXY protocol，也不得在公网入口无条件启用，应仅在已验证且受信任的前置代理链路中使用。

## 使用教程

以下流程适用于新部署，也可用于将已有 Caddy 站点迁移为仅允许 Cloudflare 回源。

1. 准备 DNS 与网络：将需要保护的域名指向源站，并在 Cloudflare 启用代理；确保源站的 `80`、`443` 端口可被 Cloudflare 回源访问。普通直连站点不应使用下方 matcher。
2. 准备配置：复制 `examples/Caddyfile.docker.example` 为根目录 `Caddyfile`，将示例域名和占位响应替换为实际站点与 `reverse_proxy`。受保护站点必须保留以下拒绝规则：

   ```caddyfile
   @not_cloudflare not cloudflare_origin
   respond @not_cloudflare "Forbidden" 403
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

5. 验证防护行为：通过 Cloudflare 代理访问受保护域名应获得正常业务响应；直接访问源站、灰云解析，或向源站请求中伪造 `CF-Connecting-IP`、`X-Forwarded-For`，均应命中 `403`。IPv4 与 IPv6 应分别验证。

6. 验证真实客户端 IP：通过 Cloudflare 的请求可由 Caddy `trusted_proxies` 处理 `CF-Connecting-IP`；直连请求的伪造 Header 不会影响 `cloudflare_origin` matcher 的判断。若结果不符合预期，优先检查 Caddy 实际收到的 TCP peer 是否被 Docker 或前置代理改写。

## 实测结果

在公开网络、Docker 化 Caddy 与 Cloudflare 代理的端到端验证中，已确认以下行为：

- 自定义 Caddy v2.11.4 构建可加载 app、IP source 与 matcher 三个模块。
- IPv4 和 IPv6 的 Cloudflare 代理请求均通过 matcher，并由 Caddy 恢复真实客户端 IP。
- IPv4 和 IPv6 的直连请求均被拒绝；即使附加伪造的 `CF-Connecting-IP` 和 `X-Forwarded-For` 也无法绕过。
- 直接连接源站但携带受保护域名的 Host/SNI 仍会被拒绝。
- 连续执行多次 Caddy reload 后，允许/拒绝语义保持不变。
- 首次网络加载成功后，LKG 缓存已生成并以 `0600` 权限保存。

这些结果只覆盖测试时的网络拓扑。实际部署仍必须按上节验证 `RemoteAddr`、IPv4、IPv6、Cloudflare 回源模式和前置代理链路。

## Caddyfile 配置

完整示例：

```caddyfile
{
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

# 受保护站点：非 Cloudflare CDN 来源返回 403。
protected.example.test {
	@not_cloudflare not cloudflare_origin
	respond @not_cloudflare "Forbidden" 403

	reverse_proxy 127.0.0.1:8080
}

# 普通站点：不使用 Cloudflare 来源 matcher，按普通 Caddy 站点处理。
direct.example.test {
	reverse_proxy 127.0.0.1:8081
}
```

`cloudflare_origin` 全局块是可选的；可配置项只有：

- `refresh_interval`：周期刷新间隔，默认 `1h`；
- `timeout`：每次抓取请求的超时，默认 `10s`；
- `max_stale`：网络不可用时允许使用的 LKG（Last Known Good）缓存最长年龄，默认 `720h`（30 天）。

`servers { trusted_proxies cloudflare }` 是可选的 Caddy 全局配置，但只有配置后 Caddy 才会把 Cloudflare 网段作为可信代理来源，用于处理 `X-Forwarded-For`、`X-Forwarded-Proto`、`CF-Connecting-IP` 等代理头。`trusted_proxies` 只负责 Caddy 的真实客户端 IP 与代理头处理，不能代替入口 ACL；来源拒绝仍必须使用 matcher 配合 `403` 规则。

插件本身不把 `CF-Connecting-IP` 当作来源认证依据。matcher 只匹配 TCP `RemoteAddr`，因此应结合实际部署链路验证 Caddy 看到的连接 peer。

## 工作方式与默认策略

- 插件从 Cloudflare 官方固定端点获取完整的 IPv4 和 IPv6 CDN ranges，并校验后发布为不可变快照；更新周期带有少量抖动。两个端点的更新是全量事务，只有 IPv4 与 IPv6 都成功获取并校验后才会发布新快照。
- 启动时优先请求网络。网络抓取或解析失败时，才回退到本地 LKG 缓存；网络成功取得的新快照会原子写入缓存。
- 默认缓存文件为 `Caddy AppDataDir/cloudflare_origin/lkg.json`，缓存文件权限为 `0600`。具体 `AppDataDir` 由 Caddy 运行环境决定。
- 只有不超过 `max_stale` 的有效缓存才会回退使用。网络和缓存都不可用、缓存损坏或已过期时，插件不会生成一个“允许全部来源”的快照，而会让配置初始化失败（fail-closed）。
- 周期刷新失败时保留当前已发布快照，不会因一次临时网络错误放宽防护。

matcher 只判断 TCP 连接的 `RemoteAddr` 是否属于当前 Cloudflare CDN 网段快照。它不根据 Host、URL、User-Agent、TLS 指纹或请求头判断来源。插件本身只提供 matcher 和 IP source；只有将 `@not_cloudflare not cloudflare_origin` 与 `respond @not_cloudflare "Forbidden" 403`（或等效拒绝处理）一同配置，非 Cloudflare 来源才会被拒绝。

## 防护边界

本插件用于收紧源站的 HTTP/HTTPS 入口，防止非 Cloudflare 官方 CDN 网段直接访问受保护站点。它**不**防护以下情况：

- 历史 DNS 记录、泄露过的旧 IP，或其他仍指向源站的解析记录；
- 其他域名、其他端口，或未在 Caddy 中使用 matcher 的站点；
- TCP/TLS 端口扫描、网络层攻击、资源耗尽或 DDoS；
- 绕过当前 Caddy HTTP/HTTPS 入口的访问路径。

Cloudflare WARP 流量不以 AS13335 判断。插件只使用 Cloudflare 官方发布的 CDN ranges；是否属于 WARP、ASN 或其他 Cloudflare 网络不会额外改变 matcher 结果。

## 外部增强与非目标

AOP、操作系统 firewall、防火墙规则和 Cloudflare Tunnel 可以作为额外的外部防护层，但它们不是本插件的一部分，也不由本插件配置或管理。需要更强的源站隐藏、网络层限制或 Cloudflare 边缘策略时，应单独部署相应方案。

本项目当前不提供 DNS 管理、Cloudflare API Token 管理、Tunnel、WAF、Enterprise 功能、AOP 或 OS firewall 集成，也不提供 metrics、admin API 等额外接口。

当前版本提供的是 `cloudflare_origin` matcher；不提供 `cloudflare_only` 语法糖。请使用 `@not_cloudflare not cloudflare_origin` 配合 `respond ... 403` 明确配置拒绝规则。
