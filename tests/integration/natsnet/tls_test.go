package natsnet_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/nats-io/nats-server/v2/server"
)

// TestMutualTLS 验证 CA 校验、ServerName、客户端证书以及缺失证书失败路径。
func TestMutualTLS(t *testing.T) {
	// 测试运行时生成独立 CA、服务端证书和客户端证书，不依赖仓库固定私钥。
	files := generateTLSFiles(t)
	serverCertificate, err := tls.LoadX509KeyPair(files.serverCert, files.serverKey)
	if err != nil {
		t.Fatalf("LoadX509KeyPair(server) error = %v", err)
	}
	caPEM, err := os.ReadFile(files.ca)
	if err != nil {
		t.Fatalf("ReadFile(CA) error = %v", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		t.Fatal("测试 CA 无法加入证书池")
	}

	// RequireAndVerifyClientCert 同时验证 natsnet 的双向 TLS 配置。
	serverOptions := defaultServerOptions()
	serverOptions.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	serverOptions.TLS = true
	serverOptions.TLSVerify = true
	running := startServer(t, serverOptions)
	tlsURL := "tls://" + running.Addr().String()

	options := testOptions("integration.mtls", tlsURL)
	options.TLS.Enabled = true
	options.TLS.CAFile = files.ca
	options.TLS.CertFile = files.clientCert
	options.TLS.KeyFile = files.clientKey
	options.TLS.ServerName = "localhost"
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	// 完成一次真实 TLS Publish/Subscribe，避免只验证握手而遗漏协议读写。
	received := make(chan struct{}, 1)
	_, err = conn.Subscribe(
		context.Background(),
		"origin.integration.mtls",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) { received <- struct{}{} },
	)
	if err != nil {
		t.Fatalf("TLS Subscribe() error = %v", err)
	}
	if err = conn.Publish("origin.integration.mtls", []byte("secure")); err != nil {
		t.Fatalf("TLS Publish() error = %v", err)
	}
	select {
	case <-received:
	case <-time.After(integrationTimeout):
		t.Fatal("TLS 消息未收到")
	}

	// 移除客户端证书后，要求 mTLS 的 Server 必须拒绝初始连接。
	withoutClient := options
	withoutClient.Name = "integration.mtls.no-client"
	withoutClient.TLS.CertFile = ""
	withoutClient.TLS.KeyFile = ""
	failed, err := natsnet.Connect(context.Background(), withoutClient, nil)
	if failed != nil || !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("缺失客户端证书 Connect() = %v, %v", failed, err)
	}
}

// tlsFiles 保存测试运行时生成的证书文件路径。
type tlsFiles struct {
	ca         string
	serverCert string
	serverKey  string
	clientCert string
	clientKey  string
}

// generateTLSFiles 创建只供当前测试使用的 CA、服务端和客户端证书。
func generateTLSFiles(t *testing.T) tlsFiles {
	t.Helper()

	tempDir := t.TempDir()
	now := time.Now()

	// 自签名 CA 只在当前测试进程有效，路径和私钥都由 TempDir 自动清理。
	caKey := generatePrivateKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Origin M6 Test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign |
			x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(
		rand.Reader,
		caTemplate,
		caTemplate,
		&caKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("CreateCertificate(CA) error = %v", err)
	}
	caFile := filepath.Join(tempDir, "ca.pem")
	writePEM(t, caFile, "CERTIFICATE", caDER)

	// 服务端证书同时包含 localhost DNS 和 127.0.0.1 IP，覆盖显式 ServerName 校验。
	serverCert, serverKey := generateLeafCertificate(
		t,
		tempDir,
		"server",
		2,
		"localhost",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		[]net.IP{net.ParseIP("127.0.0.1")},
		caTemplate,
		caKey,
	)
	clientCert, clientKey := generateLeafCertificate(
		t,
		tempDir,
		"client",
		3,
		"origin-client",
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		nil,
		caTemplate,
		caKey,
	)
	return tlsFiles{
		ca:         caFile,
		serverCert: serverCert,
		serverKey:  serverKey,
		clientCert: clientCert,
		clientKey:  clientKey,
	}
}

// generateLeafCertificate 创建一份由测试 CA 签发的叶子证书和私钥文件。
func generateLeafCertificate(
	t *testing.T,
	tempDir string,
	name string,
	serial int64,
	commonName string,
	usages []x509.ExtKeyUsage,
	ips []net.IP,
	ca *x509.Certificate,
	caKey *ecdsa.PrivateKey,
) (string, string) {
	t.Helper()

	privateKey := generatePrivateKey(t)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
		IPAddresses:  ips,
	}
	if commonName == "localhost" {
		template.DNSNames = []string{"localhost"}
	}
	certDER, err := x509.CreateCertificate(
		rand.Reader,
		template,
		ca,
		&privateKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("CreateCertificate(%s) error = %v", name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(%s) error = %v", name, err)
	}

	certFile := filepath.Join(tempDir, name+".pem")
	keyFile := filepath.Join(tempDir, name+".key")
	writePEM(t, certFile, "CERTIFICATE", certDER)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile
}

// generatePrivateKey 创建速度快且适合测试证书的 P-256 私钥。
func generatePrivateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}
	return privateKey
}

// writePEM 以仅当前用户可读权限写入测试证书或私钥。
func writePEM(t *testing.T, path, blockType string, data []byte) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("OpenFile(%s) error = %v", filepath.Base(path), err)
	}
	if err = pem.Encode(file, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		file.Close()
		t.Fatalf("pem.Encode(%s) error = %v", filepath.Base(path), err)
	}
	if err = file.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", filepath.Base(path), err)
	}
}

// 编译期确认测试直接使用真实 server.Options TLS 字段。
var _ = server.Options{}
