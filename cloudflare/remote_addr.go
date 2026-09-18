package cloudflare

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// ParseRemoteAddr 解析标准 host:port 形式的远端地址。
func ParseRemoteAddr(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("解析远端地址: %w", err)
	}
	host, _, _ = strings.Cut(host, "%")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("解析远端 IP: %w", err)
	}
	return addr.Unmap(), nil
}

// ContainsRemoteAddr 报告远端地址是否属于快照；格式错误时同时返回错误。
func ContainsRemoteAddr(snapshot *RangeSet, remoteAddr string) (bool, error) {
	addr, err := ParseRemoteAddr(remoteAddr)
	if err != nil {
		return false, err
	}
	return snapshot.Contains(addr), nil
}
