# caddy-cloudflare-origin-guard

一个面向 Caddy v2 的 Cloudflare 源站访问防护插件。它定期读取 Cloudflare 官方公布的 IPv4/IPv6 CDN 网段，在 Caddy 中同时提供：

- 当前 Caddy 应用模块：`cloudflare_origin`；
- HTTP matcher：`cloudflare_origin`（模块 ID：`http.matchers.cloudflare_origin`）；
- Caddy `trusted_proxies` 使用的 IP source：`cloudflare`（模块 ID：`http.ip_sources.cloudflare`）。

它的防护目标是：在源站 HTTP/HTTPS 端口前，拒绝直接来自非 Cloudflare 官方 CDN 网段的请求，并让经过 Cloudflare 代理的请求能够由 Caddy 正确恢复客户端 IP。插件不代理流量，也不改变 DNS、证书或 Cloudflare 账号配置。

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
	}
}

# 受保护站点：非 Cloudflare CDN 来源返回 403。
protected.example.com {
	@not_cloudflare not cloudflare_origin
	respond @not_cloudflare "Forbidden" 403

	reverse_proxy 127.0.0.1:8080
}

# 普通站点：不使用 Cloudflare 来源 matcher，按普通 Caddy 站点处理。
普通.example.com {
	reverse_proxy 127.0.0.1:8081
}
```

`cloudflare_origin` 全局块是可选的；可配置项只有：

- `refresh_interval`：周期刷新间隔，默认 `1h`；
- `timeout`：每次抓取请求的超时，默认 `10s`；
- `max_stale`：网络不可用时允许使用的 LKG（Last Known Good）缓存最长年龄，默认 `720h`（30 天）。

`servers { trusted_proxies cloudflare }` 是可选的 Caddy 全局配置，但只有配置后 Caddy 才会把 Cloudflare 网段作为可信代理来源，用于处理 `X-Forwarded-For`、`X-Forwarded-Proto` 等代理头。插件本身不读取 `CF-Connecting-IP`，也不把该请求头当作来源认证依据；客户端 IP 的恢复完全交给 Caddy 的 `trusted_proxies` 机制。

## 工作方式与默认策略

- 插件从 Cloudflare 官方固定端点获取完整的 IPv4 和 IPv6 CDN ranges，并校验后发布为不可变快照；更新周期带有少量抖动。
- 启动时优先请求网络。网络抓取或解析失败时，才回退到本地 LKG 缓存；网络成功取得的新快照会原子写入缓存。
- 默认缓存文件为 `Caddy AppDataDir/cloudflare_origin/lkg.json`，缓存文件权限为 `0600`。具体 `AppDataDir` 由 Caddy 运行环境决定。
- 只有不超过 `max_stale` 的有效缓存才会回退使用。网络和缓存都不可用、缓存损坏或已过期时，插件不会生成一个“允许全部来源”的快照，而会让配置初始化失败（fail-closed）。
- 周期刷新失败时保留当前已发布快照，不会因一次临时网络错误放宽防护。

matcher 只判断 TCP 连接的 `RemoteAddr` 是否属于当前 Cloudflare CDN 网段快照。它不根据 Host、URL、User-Agent、TLS 指纹或请求头判断来源。

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
