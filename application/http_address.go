package application

import (
	"net"
	"strings"
)

// isLoopbackAddress 判断显式 host:port 是否只绑定到本机回环地址。
func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
