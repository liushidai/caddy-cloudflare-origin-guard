# caddy-cloudflare-origin-guard

English | [简体中文](README.md)

A Cloudflare origin access protection plugin for Caddy v2. It periodically fetches the IPv4/IPv6 CDN ranges officially published by Cloudflare and provides all of the following in Caddy:

- The current Caddy app module: `cloudflare_origin`;
- HTTP matcher: `cloudflare_origin` (module ID: `http.matchers.cloudflare_origin`);
- The IP source used by Caddy `trusted_proxies`: `cloudflare` (module ID: `http.ip_sources.cloudflare`).

Its protection goal is: in front of the origin's HTTP/HTTPS ports, block requests coming directly from non-Cloudflare official CDN ranges through the matcher and explicit deny rules, and let Caddy correctly restore the client IP for requests proxied through Cloudflare. The plugin does not proxy traffic, nor does it change DNS, certificates, or Cloudflare account configuration.

## Installation and Build

Use [xcaddy](https://github.com/caddyserver/xcaddy) to compile the plugin into Caddy. It currently depends on Caddy v2.11.4:

```bash
xcaddy build v2.11.4 \
  --with github.com/liushidai/caddy-cloudflare-origin-guard=.
```

The command above is executed in the plugin source directory, the same way the repository's `Dockerfile` compiles a local checkout. A production build should first obtain and review the complete source commit; for example, run the following in a separate build directory:

```bash
SOURCE_COMMIT="<经审核的40位完整提交哈希>"
git clone https://github.com/liushidai/caddy-cloudflare-origin-guard.git source-checkout
cd source-checkout
git checkout --detach "$SOURCE_COMMIT"
xcaddy build v2.11.4 --with github.com/liushidai/caddy-cloudflare-origin-guard=.
```

Replace the Caddy binary actually running with the generated one, and use `caddy list-modules` to check that it contains the three modules above. Docker builds likewise compile the local checkout, so check out the reviewed commit before building the image. Pinning the source commit is not the same as pinning the built image's tag/digest, and it does not guarantee byte-for-byte reproducibility; a production image can further pin a reviewed digest.

## Docker Deployment

The repository root provides an optional Docker Compose deployment example. First copy and edit the sanitized example, then start the services:

```bash
cp examples/Caddyfile.docker.example Caddyfile
# Edit the Caddyfile: replace the example domain, and change the placeholder respond to the actual site configuration.
docker compose up -d --build
```

You can also build only the image:

```bash
docker build -t caddy-cloudflare-origin-guard .
```

`compose.yaml` is only a demonstration configuration and cannot directly replace a production deployment. It has Compose manage a single `caddy_bridge` dual-stack bridge network (`enable_ipv6: true`), explicitly publishes `80:80/tcp`, `443:443/tcp`, and `443:443/udp` (HTTP/3), mounts the root `./Caddyfile` read-only at `/etc/caddy/Caddyfile`, and persists `/data` and `/config` through named volumes. The actual network name usually carries a Compose project name prefix; a fixed network name, IPAM address, or machine path is not required. `/data` contains runtime data such as certificates and the plugin's LKG (Last Known Good) address cache; deleting or not persisting it loses this state.

If the same Compose project already has a `photo` service, just add the network configuration to the **existing service**; this snippet does not create a service or specify an image:

```yaml
services:
  photo:
    networks:
      - caddy_bridge
```

Only when `caddy` and `photo` are on the same network can the HTTPS site use `reverse_proxy photo:8080` to resolve and reach it; publishing host ports alone does not provide service-name resolution between containers. At container runtime, outbound access to Cloudflare's official IPv4 and IPv6 range endpoints is required; both the first start and subsequent updates depend on that access, and when the network is unavailable, initialization can only proceed when a valid LKG exists and has not exceeded `max_stale`.

Changing an existing Compose network from IPv4-only to dual-stack requires recreating the network and recreating the related containers, which may cause downtime; back up `/data` in advance, keep the original Compose project name and named volumes, schedule a maintenance window, and do not run `docker compose down -v`. `enable_ipv6` only guarantees that the network assigns IPv6 addresses; it does not guarantee that all Docker gateway modes and platforms use IPv6 DNAT; before going live, you must check the network configuration in use and the IPv6 TCP/UDP forwarding path on site, and send requests from a genuinely external IPv6 client to check `request.remote_ip` in the Caddy logs. When a host IPv6 port is published on an IPv4-only bridge, `docker-proxy` may rewrite the true IPv6 source to the bridge address; the plugin can only judge the `RemoteAddr` that Caddy actually receives, and cannot restore the original peer of the upstream connection. Never circumvent this problem by adding `172.16.0.0/12` to trusted proxies or allowed sources. localhost forwarding, Docker Desktop, rootless Docker, other userland proxies, load balancers, etc. may also rewrite the peer. A single Linux host entry point may choose host network for special topologies; that mode cannot be used together with `ports` and is not the Compose default configuration. If the entry point receives the PROXY protocol, it must not be enabled unconditionally on a public entry point, and should be used only in a chain of verified and trusted front proxies.

## Usage Tutorial

The following flow applies to new deployments, and can also be used to migrate an existing Caddy site so that only Cloudflare can reach the origin.

1. Prepare DNS and networking: point the domains to be protected to the origin and enable proxying in Cloudflare; ensure the origin's `80/tcp` and `443/tcp` (plus `443/udp` if HTTP/3 is used) are reachable as expected. Ordinary direct-connect sites should not use the matcher below.
2. Prepare the configuration: copy `examples/Caddyfile.docker.example` to the root `Caddyfile`, and replace the example domain and the HTTPS site's placeholder response with the actual site and `reverse_proxy`. Both HTTP and HTTPS must first deny non-Cloudflare sources inside `route`; the HTTP site redirects only after the guard passes:

   ```caddyfile
   route {
       @not_cloudflare not cloudflare_origin
       respond @not_cloudflare "Forbidden" 403
       # Place the site's subsequent handling here (HTTP redirect or HTTPS business handling).
   }
   ```

3. Build and start: run the following in the repository root:

   ```bash
   docker compose up -d --build
   ```

4. Confirm module and service status:

   ```bash
   docker compose exec caddy caddy list-modules | grep cloudflare
   docker compose ps
   docker compose logs caddy
   ```

   The output should contain `cloudflare_origin`, `http.ip_sources.cloudflare`, and `http.matchers.cloudflare_origin`. The first load must successfully fetch the official ranges, or use a non-expired LKG cache; when neither is available, Caddy refuses to load the configuration.

5. Verify protection behavior: accessing the protected domain through the Cloudflare proxy should return the normal business response; truly external clients directly accessing the origin over HTTP or HTTPS, or forging `CF-Connecting-IP` and `X-Forwarded-For`, should all hit `403`. The specific check commands for IPv4, IPv6, and HTTP/3 are in the next section.

6. Verify the true connection source: for requests through Cloudflare, Caddy `trusted_proxies` can handle `CF-Connecting-IP`; forged headers on direct requests should not affect the matcher. Check whether `request.remote_ip` in the JSON access log is the IP of the genuinely external connection; `request.client_ip` may be derived from proxy headers and cannot be used as evidence of the peer.

## Pre-Deployment Connectivity and Protocol Verification

The example domain `protected.example.test` and the origin addresses `192.0.2.10` and `2001:db8::10` are all documentation placeholders; replace them with the real domain and real origin addresses before executing. The direct-connect commands below must be executed on a **genuinely external** IPv4/IPv6 client; do not test on the host machine or a container's local loopback. The HTTPS certificate should be trusted by the test client; configure `--cacert` when necessary, and do not mistake TLS errors for the result of the source guard. `--resolve` preserves both the request Host and the TLS SNI, and `--noproxy '*'` prevents environment proxies from interfering:

```bash
curl --noproxy '*' -4 -i --resolve protected.example.test:80:192.0.2.10 http://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:80:[2001:db8::10]' http://protected.example.test/
curl --noproxy '*' -4 -i --resolve protected.example.test:443:192.0.2.10 https://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:443:[2001:db8::10]' https://protected.example.test/
curl --noproxy '*' -6 -i --resolve 'protected.example.test:443:[2001:db8::10]' \
  -H 'CF-Connecting-IP: 192.0.2.10' -H 'X-Forwarded-For: 192.0.2.10' https://protected.example.test/
```

All the direct connections above should return `403`, especially requests with forged headers. Check `request.remote_ip` in the corresponding JSON access log in `docker compose logs caddy`: it must be the IP of the externally initiated request, not a Docker bridge address; `request.client_ip` (which may come from `CF-Connecting-IP`) is not evidence of the peer. Accessing HTTPS through the Cloudflare entry should return the normal business response; the Cloudflare edge may redirect HTTP by itself, so a response over HTTP at the edge cannot prove that the origin HTTP guard has been tested. When accessing an orange-clouded domain, `curl -4`/`curl -6` only select the address family from the client to the Cloudflare edge; you cannot assert from that which address family the edge uses to reach the origin.

Check host IPv6 NAT: choose the read-only command `sudo ip6tables -t nat -S DOCKER` or `sudo nft list ruleset` according to the actual firewall backend, and verify the IPv6 publishing and forwarding/NAT paths for `80/tcp`, `443/tcp`, and `443/udp`; not all Docker gateway modes use DNAT. A rule listing is not sufficient evidence that traffic truly arrives and that the source has not been rewritten; it must also be verified with external IPv6 requests, host packet captures when necessary, and the access logs above. For HTTP/3, first confirm that the client supports HTTP3 with `curl -V`, then force QUIC on the external client:

```bash
curl --noproxy '*' -4 --http3-only -i --resolve protected.example.test:443:192.0.2.10 https://protected.example.test/
curl --noproxy '*' -6 --http3-only -i --resolve 'protected.example.test:443:[2001:db8::10]' https://protected.example.test/
```

The direct connections should all return `403`, and the corresponding JSON logs should confirm that `request.proto` is `HTTP/3.0` (or the actual HTTP/3 protocol marker output by Caddy); `--http3-only` does not allow silently falling back to TCP. Accessing HTTP/3 through the orange cloud only proves the QUIC link from the client to the edge; it does not prove that the edge uses HTTP/3 or IPv6 to reach the origin.

## Test Results

In a previous end-to-end verification on a public network, with Dockerized Caddy and a Cloudflare proxy, the following behaviors were confirmed (limited to the topology and protocols tested at that time, and not to be regarded as a re-test of this dual-stack network and HTTP/3):

- A custom Caddy v2.11.4 build can load the app, IP source, and matcher modules.
- Cloudflare-proxied requests over both IPv4 and IPv6 passed the matcher, and Caddy restored the real client IP.
- Direct requests over both IPv4 and IPv6 were rejected; even attaching forged `CF-Connecting-IP` and `X-Forwarded-For` could not bypass the guard.
- Connecting directly to the origin while carrying the protected domain's Host/SNI was still rejected.
- After several consecutive Caddy reloads, the allow/deny semantics remained unchanged.
- After the first successful network load, the LKG cache was generated and saved with `0600` permissions.

It was also previously observed that the automatic HTTP-to-HTTPS redirect may handle HTTP requests before the HTTPS site's matcher. The `auto_https disable_redirects` and the explicit protected HTTP site above are configuration fixes for that behavior, and do not mean that this configuration has been re-tested. Actual deployments must still verify HTTP, HTTPS, HTTP/3 (if enabled), `RemoteAddr`, the Cloudflare origin-pull mode, and the front-proxy chain according to the previous section.

## Caddyfile Configuration

Complete example:

```caddyfile
{
	auto_https disable_redirects

	# Optional. When omitted, the defaults are used:
	# refresh_interval 1h, timeout 10s, max_stale 720h (30 days).
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
		# Replace the next line with reverse_proxy photo:8080 (the two must be on the same Compose network).
		respond "受保护站点占位响应" 200
	}
}
```

The `route` fixes the deny rule before the HTTP redirect or HTTPS business handling, preventing the default directive ordering from letting the redirect bypass the guard. Caddy's automatically generated HTTP-to-HTTPS redirect does not pass through the HTTPS site's matcher; if HTTP must also respond only to Cloudflare, use the global `auto_https disable_redirects` and explicitly configure a protected `http://` site. That option turns off the automatic HTTP-to-HTTPS redirect for **other ordinary sites in this configuration**, and they need their own desired HTTP behavior configured separately; it does not mean the plugin will intercept ACME challenges or all port 80 responses. A site declared by domain name also does not constrain unmatched Hosts. Automatic certificate management may still use HTTP-01 or TLS-ALPN-01 challenges; confirm according to the actual certificate approach that validation traffic can reach, and use DNS-01 when necessary. The `log` above outputs each site's JSON access log to stdout, making it convenient to check `request.remote_ip`.

The global `cloudflare_origin` block is optional; the only configurable items are:

- `refresh_interval`: periodic refresh interval, default `1h`;
- `timeout`: timeout for each fetch request, default `10s`;
- `max_stale`: the maximum age allowed for falling back to the local LKG (Last Known Good) cache when, at initialization, the network is unavailable, default `720h` (30 days); it does not limit the lifetime of an already published runtime in-memory snapshot.

`servers { trusted_proxies cloudflare }` is an optional Caddy global configuration, but only after it is configured will Caddy treat Cloudflare ranges as trusted proxy sources, used to handle proxy headers such as `X-Forwarded-For`, `X-Forwarded-Proto`, and `CF-Connecting-IP`. `trusted_proxies` is only responsible for Caddy's real client IP and proxy header handling, and cannot replace an entry-point ACL; source denial must still use the matcher together with a `403` rule.
Do not add Docker bridge ranges (such as `172.16.0.0/12`) to trusted proxies or source allow lists.

The plugin itself does not use `CF-Connecting-IP` as the basis for source authentication. The matcher only matches the `RemoteAddr` of the connection Caddy receives (including the connection source for HTTP/3), so the peer Caddy sees should be verified against the actual deployment path; an original source that has been rewritten by a proxy cannot be restored by the plugin.

## How It Works and Default Policy

- The plugin fetches the complete IPv4 and IPv6 CDN ranges from Cloudflare's official fixed endpoints, validates them, and publishes them as an immutable snapshot; the update cycle has a small amount of jitter. The updates from the two endpoints are an all-or-nothing transaction, and a new snapshot is published only after both IPv4 and IPv6 are successfully fetched and validated.
- At startup, the network is requested first. Only when the network fetch or parsing fails does it fall back to the local LKG cache; a new snapshot successfully obtained from the network is written atomically to the cache.
- The default cache file is `Caddy AppDataDir/cloudflare_origin/lkg.json`, and the cache file permissions are `0600`. The specific `AppDataDir` is determined by the Caddy runtime environment.
- At initialization, only a valid cache not older than `max_stale` will be used as a fallback. When the network and the cache are both unavailable, or the cache is corrupted or expired, the plugin will not generate an "allow all sources" snapshot; instead it makes configuration initialization fail (fail-closed). `max_stale` is not an expiry threshold for an already published in-memory snapshot at runtime.
- When a periodic refresh fails, the currently published snapshot is retained, and protection will not be loosened by a single temporary network error.

The matcher only determines whether the `RemoteAddr` of the connection Caddy receives belongs to the current Cloudflare CDN range snapshot. It does not determine the source from Host, URL, User-Agent, TLS fingerprint, or request headers. The plugin itself only provides the matcher and the IP source; non-Cloudflare sources are rejected only when `@not_cloudflare not cloudflare_origin` is configured together with `respond @not_cloudflare "Forbidden" 403` (or equivalent deny handling).

## Protection Boundaries

This plugin is an HTTP site-level guard, not a network-layer firewall; it is used to tighten the HTTP/HTTPS (including HTTP/3 when enabled) requests to protected sites on the origin. It does **not** protect against the following:

- Historical DNS records, old leaked IPs, or other resolution records still pointing to the origin;
- Other domains, other ports, or sites that do not use the matcher in Caddy;
- TCP/TLS port scanning, network-layer attacks, resource exhaustion, or DDoS;
- Access paths that bypass the current Caddy HTTP/HTTPS entry point.

Cloudflare WARP traffic is not determined by AS13335. The plugin uses only the CDN ranges officially published by Cloudflare; whether traffic belongs to WARP, an ASN, or another Cloudflare network does not additionally change the matcher result.

## External Enhancements and Non-Goals

AOP, operating system firewalls, firewall rules, and Cloudflare Tunnel can serve as additional external protection layers, but they are not part of this plugin, nor are they configured or managed by it. When stronger origin hiding, network-layer restrictions, or Cloudflare edge policies are needed, the corresponding solutions should be deployed separately.

This project currently does not provide DNS management, Cloudflare API Token management, Tunnel, WAF, Enterprise features, AOP, or OS firewall integration, nor does it provide additional interfaces such as metrics or an admin API.

The current version provides the `cloudflare_origin` matcher; it does not provide the `cloudflare_only` syntactic sugar. Use `@not_cloudflare not cloudflare_origin` together with `respond ... 403` to explicitly configure the deny rule.
