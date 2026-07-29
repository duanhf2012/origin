package application

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/node"
	"go.etcd.io/etcd/server/v3/embed"
)

func TestApplicationEtcdDiscoveryLifecycle(t *testing.T) {
	endpoint := startApplicationEtcd(t)
	directory := writeApplicationConfig(t, `
discovery:
  type: etcd
  etcd:
    endpoints: [`+endpoint+`]
    local_network: cn-east
    ttl: 3s
    request_timeout: 3s
nodes:
  - id: game-1
    services: [lifecycleTestService]
`)
	app := newSilentApplication()
	app.Setup(&lifecycleTestService{})
	runCtx, cancelRun := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "etcd-discovery-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)
	current, exists := app.Node("game-1")
	if !exists {
		t.Fatal("Node game-1 不存在")
	}
	status := current.DiscoveryStatus()
	if status.Kind != "etcd" || status.State != node.DiscoveryReady ||
		!status.Synchronized ||
		status.Publication != node.PublicationPublished {
		t.Fatalf("DiscoveryStatus = %+v", status)
	}
	cancelRun()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func startApplicationEtcd(t *testing.T) string {
	t.Helper()
	clientURL := reserveApplicationEtcdURL(t)
	peerURL := reserveApplicationEtcdURL(t)
	config := embed.NewConfig()
	config.Dir = t.TempDir()
	config.LogLevel = "error"
	config.LogOutputs = []string{os.DevNull}
	config.ListenClientUrls = []url.URL{clientURL}
	config.AdvertiseClientUrls = []url.URL{clientURL}
	config.ListenPeerUrls = []url.URL{peerURL}
	config.AdvertisePeerUrls = []url.URL{peerURL}
	config.InitialCluster = config.InitialClusterFromName(config.Name)
	server, err := embed.StartEtcd(config)
	if err != nil {
		t.Fatalf("StartEtcd() error = %v", err)
	}
	t.Cleanup(server.Close)
	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		server.Server.Stop()
		t.Fatal("embedded etcd 启动超时")
	}
	return clientURL.String()
}

func reserveApplicationEtcdURL(t *testing.T) url.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve etcd address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved address: %v", err)
	}
	parsed, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatalf("parse reserved URL: %v", err)
	}
	return *parsed
}
