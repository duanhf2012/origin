package kcpnet

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"strings"

	kcplib "github.com/xtaci/kcp-go/v5"
)

// Dial 创建一个本地 KCP Session；它不执行远端握手，也不证明对端正在监听。
func Dial(
	ctx context.Context,
	address string,
	options DialOptions,
	handler Handler,
) (*Conn, error) {
	if ctx == nil {
		return nil, invalidArgument("kcpnet: Dial Context 不能为空")
	}
	if strings.TrimSpace(address) == "" {
		return nil, invalidArgument("kcpnet: Dial 地址不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("kcpnet: Dial Handler 不能为空")
	}
	if err := validateDialOptions(options); err != nil {
		return nil, err
	}
	remote, network, err := resolveUDPAddress(ctx, address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextError(ctx.Err())
		}
		return nil, transportUnavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, contextError(err)
	}
	packetConn, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, transportUnavailable(err)
	}
	var conversation uint32
	if err := binary.Read(cryptorand.Reader, binary.LittleEndian, &conversation); err != nil {
		_ = packetConn.Close()
		return nil, transportUnavailable(err)
	}
	raw, err := kcplib.NewConn4(
		conversation,
		remote,
		options.BlockCrypt,
		options.FEC.DataShards,
		options.FEC.ParityShards,
		true,
		packetConn,
	)
	if err != nil {
		_ = packetConn.Close()
		return nil, transportUnavailable(err)
	}
	if err := configureDialSocket(raw, options); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := configureSession(raw, options.Connection.Protocol); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = raw.Close()
		return nil, contextError(err)
	}
	conn := newConn(raw, options.Connection, handler, nil)
	conn.start()
	return conn, nil
}

func configureDialSocket(raw *kcplib.UDPSession, options DialOptions) error {
	if options.DSCP > 0 {
		if err := raw.SetDSCP(options.DSCP); err != nil {
			return transportUnavailable(err)
		}
	}
	if options.SocketReadBuffer > 0 {
		if err := raw.SetReadBuffer(options.SocketReadBuffer); err != nil {
			return transportUnavailable(err)
		}
	}
	if options.SocketWriteBuffer > 0 {
		if err := raw.SetWriteBuffer(options.SocketWriteBuffer); err != nil {
			return transportUnavailable(err)
		}
	}
	return nil
}

func resolveUDPAddress(ctx context.Context, address string) (*net.UDPAddr, string, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, "", err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		port, err = net.DefaultResolver.LookupPort(ctx, "udp", portText)
		if err != nil {
			return nil, "", err
		}
	}
	if port < 0 || port > 65535 {
		return nil, "", &net.AddrError{Err: "invalid port", Addr: address}
	}
	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		network := "udp4"
		if literal.Is6() {
			network = "udp6"
		}
		return &net.UDPAddr{
			IP:   net.IP(literal.AsSlice()),
			Port: port,
			Zone: literal.Zone(),
		}, network, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, "", err
	}
	if len(addresses) == 0 {
		return nil, "", &net.DNSError{Err: "no address", Name: host}
	}
	selected := addresses[0]
	network := "udp6"
	if selected.Is4() {
		network = "udp4"
	}
	return &net.UDPAddr{
		IP:   net.IP(selected.AsSlice()),
		Port: port,
		Zone: selected.Zone(),
	}, network, nil
}
