package etcd

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/discovery/providertest"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"go.uber.org/zap"
)

type integrationRecorder struct {
	mu       sync.Mutex
	ttl      time.Duration
	snapshot publicprovider.Snapshot
	report   publicprovider.Report
	sizes    []int
	changed  chan struct{}
}

func newIntegrationRecorder() *integrationRecorder {
	return &integrationRecorder{changed: make(chan struct{}, 64)}
}

func (recorder *integrationRecorder) host() publicprovider.Host {
	return publicprovider.NewHost(
		func(ttl time.Duration) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if recorder.ttl != 0 && recorder.ttl != ttl {
				return errs.ErrInvalidConfig
			}
			recorder.ttl = ttl
			return nil
		},
		func(snapshot publicprovider.Snapshot) error {
			normalized, err := publicprovider.NormalizeSnapshot(snapshot)
			if err != nil {
				return err
			}
			recorder.mu.Lock()
			recorder.snapshot = normalized
			recorder.sizes = append(recorder.sizes, len(normalized.Nodes))
			recorder.mu.Unlock()
			select {
			case recorder.changed <- struct{}{}:
			default:
			}
			return nil
		},
		func(report publicprovider.Report) {
			recorder.mu.Lock()
			recorder.report = report
			recorder.mu.Unlock()
		},
	)
}

func TestEtcdProviderRangePaginationNetworkWatchAndTransactionAtomicity(
	t *testing.T,
) {
	endpoint := startEmbeddedEtcd(t)
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("clientv3.New() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	operations := make([]clientv3.Op, 0, 40)
	for index := 0; index < 40; index++ {
		nodeID := fmt.Sprintf("game-%03d", index)
		value, encodeErr := encodeRecord(
			"cn-north",
			recordTestNode(nodeID, uint64(index+1)),
		)
		if encodeErr != nil {
			t.Fatalf("encodeRecord(%s) error = %v", nodeID, encodeErr)
		}
		operations = append(operations, clientv3.OpPut(
			"/origin/v1/networks/cn-north/nodes/"+nodeID,
			string(value),
		))
	}
	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := client.Txn(requestCtx).Then(operations...).Commit(); err != nil {
		cancel()
		t.Fatalf("seed transaction error = %v", err)
	}
	cancel()

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":      []string{endpoint},
		"local_network":  "cn-east",
		"watch_networks": []string{"cn-north"},
		"ttl":            "3s",
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "observer-1",
		SessionID: 90,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	startProviderForTest(t, instance)
	t.Cleanup(func() { _ = instance.Close(context.Background()) })
	recorder.awaitCount(t, 40, 5*time.Second)

	eastValue, _ := encodeRecord(
		"cn-east",
		recordTestNode("east-new", 101),
	)
	northValue, _ := encodeRecord(
		"cn-north",
		recordTestNode("north-new", 102),
	)
	recorder.mu.Lock()
	historyStart := len(recorder.sizes)
	recorder.mu.Unlock()
	requestCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	_, err = client.Txn(requestCtx).Then(
		clientv3.OpPut(
			"/origin/v1/networks/cn-east/nodes/east-new",
			string(eastValue),
		),
		clientv3.OpPut(
			"/origin/v1/networks/cn-north/nodes/north-new",
			string(northValue),
		),
	).Commit()
	cancel()
	if err != nil {
		t.Fatalf("cross-network transaction error = %v", err)
	}
	recorder.awaitCount(t, 42, 5*time.Second)
	recorder.mu.Lock()
	sizes := append([]int(nil), recorder.sizes[historyStart:]...)
	recorder.mu.Unlock()
	for _, size := range sizes {
		if size == 41 {
			t.Fatalf("跨网络同 Revision 暴露了部分事务快照: %v", sizes)
		}
	}
}

func TestEtcdProviderRejectsCrossNetworkDuplicateNodeAtStart(t *testing.T) {
	endpoint := startEmbeddedEtcd(t)
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("clientv3.New() error = %v", err)
	}
	defer client.Close()
	east, _ := encodeRecord("cn-east", recordTestNode("shared-1", 1))
	north, _ := encodeRecord("cn-north", recordTestNode("shared-1", 2))
	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = client.Txn(requestCtx).Then(
		clientv3.OpPut(
			"/origin/v1/networks/cn-east/nodes/shared-1",
			string(east),
		),
		clientv3.OpPut(
			"/origin/v1/networks/cn-north/nodes/shared-1",
			string(north),
		),
	).Commit()
	cancel()
	if err != nil {
		t.Fatalf("seed duplicate transaction error = %v", err)
	}
	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":      []string{endpoint},
		"local_network":  "cn-east",
		"watch_networks": []string{"cn-north"},
		"ttl":            "3s",
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "observer-1",
		SessionID: 90,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	err = instance.Start(startCtx)
	cancelStart()
	if !errs.IsCode(err, errs.CodeDiscoverySnapshotInvalid) {
		t.Fatalf("cross-network duplicate Start() error = %v", err)
	}
	_ = instance.Close(context.Background())
}

func (recorder *integrationRecorder) awaitNode(
	t *testing.T,
	nodeID string,
	present bool,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		recorder.mu.Lock()
		found := false
		for _, node := range recorder.snapshot.Nodes {
			if node.NodeID == nodeID {
				found = true
				break
			}
		}
		recorder.mu.Unlock()
		if found == present {
			return
		}
		select {
		case <-recorder.changed:
		case <-timer.C:
			t.Fatalf("等待 Node %q present=%v 超时", nodeID, present)
		}
	}
}

func (recorder *integrationRecorder) awaitCount(
	t *testing.T,
	count int,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		recorder.mu.Lock()
		current := len(recorder.snapshot.Nodes)
		recorder.mu.Unlock()
		if current == count {
			return
		}
		select {
		case <-recorder.changed:
		case <-timer.C:
			t.Fatalf("等待 Node 数量 %d 超时，当前 %d", count, current)
		}
	}
}

func (recorder *integrationRecorder) awaitState(
	t *testing.T,
	state publicprovider.State,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		recorder.mu.Lock()
		current := recorder.report.State
		recorder.mu.Unlock()
		if current == state {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("等待 Provider State %v 超时，当前 %v", state, current)
		}
	}
}

func TestEtcdProviderRecoversAfterServerRestart(t *testing.T) {
	clientURL := reserveEtcdURL(t)
	peerURL := reserveEtcdURL(t)
	config := embed.NewConfig()
	config.Dir = t.TempDir()
	config.LogLevel = "error"
	config.LogOutputs = []string{os.DevNull}
	config.ListenClientUrls = []url.URL{clientURL}
	config.AdvertiseClientUrls = []url.URL{clientURL}
	config.ListenPeerUrls = []url.URL{peerURL}
	config.AdvertisePeerUrls = []url.URL{peerURL}
	config.InitialCluster = config.InitialClusterFromName(config.Name)
	server := startConfiguredEtcd(t, config)
	t.Cleanup(func() {
		if server != nil {
			server.Close()
		}
	})

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":       []string{clientURL.String()},
		"local_network":   "cn-east",
		"ttl":             "3s",
		"dial_timeout":    "2s",
		"request_timeout": "2s",
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 501,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	startProviderForTest(t, instance)
	t.Cleanup(func() { _ = instance.Close(context.Background()) })
	if err := instance.Publish(
		context.Background(),
		recordTestNode("game-1", 501),
	); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	server.Close()
	server = nil
	recorder.awaitState(t, publicprovider.StateRecovering, 10*time.Second)
	updated := recordTestNode("game-1", 501)
	updated.Labels["revision"] = "recovered"
	publishResult := make(chan error, 1)
	go func() {
		publishCtx, cancelPublish := context.WithTimeout(
			context.Background(),
			15*time.Second,
		)
		defer cancelPublish()
		publishResult <- instance.Publish(publishCtx, updated)
	}()
	server = startConfiguredEtcd(t, config)
	recorder.awaitState(t, publicprovider.StateReady, 15*time.Second)
	if err := <-publishResult; err != nil {
		t.Fatalf("recovery Publish() error = %v", err)
	}
	if err := instance.Withdraw(context.Background()); err != nil {
		t.Fatalf("recovered Withdraw() error = %v", err)
	}
}

func TestEtcdProviderAuthenticationAndToken(t *testing.T) {
	endpoint := startEmbeddedEtcd(t)
	admin, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("admin client error = %v", err)
	}
	defer admin.Close()
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := admin.UserAdd(setupCtx, "root", "root-password"); err != nil {
		cancelSetup()
		t.Fatalf("UserAdd(root) error = %v", err)
	}
	if _, err := admin.RoleAdd(setupCtx, "root"); err != nil {
		cancelSetup()
		t.Fatalf("RoleAdd(root) error = %v", err)
	}
	if _, err := admin.UserGrantRole(setupCtx, "root", "root"); err != nil {
		cancelSetup()
		t.Fatalf("UserGrantRole(root) error = %v", err)
	}
	if _, err := admin.AuthEnable(setupCtx); err != nil {
		cancelSetup()
		t.Fatalf("AuthEnable() error = %v", err)
	}
	cancelSetup()

	unauthorizedConfig, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":       []string{endpoint},
		"local_network":   "cn-east",
		"request_timeout": "2s",
	})
	unauthorizedRecorder := newIntegrationRecorder()
	unauthorized, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "unauthorized-1",
		SessionID: 600,
		Config:    unauthorizedConfig,
		Host:      unauthorizedRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("unauthorized Factory() error = %v", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	err = unauthorized.Start(startCtx)
	cancelStart()
	if err == nil {
		t.Fatal("无认证 Provider 意外启动成功")
	}
	_ = unauthorized.Close(context.Background())

	passwordConfig, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":     []string{endpoint},
		"local_network": "cn-east",
		"auth": map[string]any{
			"username": "root",
			"password": "root-password",
		},
	})
	passwordRecorder := newIntegrationRecorder()
	passwordProvider, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "password-1",
		SessionID: 601,
		Config:    passwordConfig,
		Host:      passwordRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("password Factory() error = %v", err)
	}
	startProviderForTest(t, passwordProvider)
	if err := passwordProvider.Close(context.Background()); err != nil {
		t.Fatalf("password Close() error = %v", err)
	}

	authCtx, cancelAuth := context.WithTimeout(context.Background(), 5*time.Second)
	authResponse, err := admin.Authenticate(authCtx, "root", "root-password")
	cancelAuth()
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	tokenConfig, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":     []string{endpoint},
		"local_network": "cn-east",
		"auth": map[string]any{
			"token": authResponse.Token,
		},
	})
	tokenRecorder := newIntegrationRecorder()
	tokenProvider, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "token-1",
		SessionID: 602,
		Config:    tokenConfig,
		Host:      tokenRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("token Factory() error = %v", err)
	}
	startProviderForTest(t, tokenProvider)
	if err := tokenProvider.Close(context.Background()); err != nil {
		t.Fatalf("token Close() error = %v", err)
	}
}

func TestEtcdProviderPrefixRBAC(t *testing.T) {
	endpoint := startEmbeddedEtcd(t)
	admin, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("admin client error = %v", err)
	}
	defer admin.Close()

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSetup()
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"add root user", func() error {
			_, runErr := admin.UserAdd(setupCtx, "root", "root-password")
			return runErr
		}},
		{"add root role", func() error {
			_, runErr := admin.RoleAdd(setupCtx, "root")
			return runErr
		}},
		{"grant root role", func() error {
			_, runErr := admin.UserGrantRole(setupCtx, "root", "root")
			return runErr
		}},
		{"add discovery user", func() error {
			_, runErr := admin.UserAdd(setupCtx, "origin-east", "origin-password")
			return runErr
		}},
		{"add discovery role", func() error {
			_, runErr := admin.RoleAdd(setupCtx, "origin-east")
			return runErr
		}},
		{"grant local read/write", func() error {
			prefix := "/origin/v1/networks/cn-east/nodes/"
			_, runErr := admin.RoleGrantPermission(
				setupCtx,
				"origin-east",
				prefix,
				clientv3.GetPrefixRangeEnd(prefix),
				clientv3.PermissionType(clientv3.PermReadWrite),
			)
			return runErr
		}},
		{"grant watched read", func() error {
			prefix := "/origin/v1/networks/cn-north/nodes/"
			_, runErr := admin.RoleGrantPermission(
				setupCtx,
				"origin-east",
				prefix,
				clientv3.GetPrefixRangeEnd(prefix),
				clientv3.PermissionType(clientv3.PermRead),
			)
			return runErr
		}},
		{"grant discovery role", func() error {
			_, runErr := admin.UserGrantRole(
				setupCtx,
				"origin-east",
				"origin-east",
			)
			return runErr
		}},
		{"enable auth", func() error {
			_, runErr := admin.AuthEnable(setupCtx)
			return runErr
		}},
	} {
		if err := operation.run(); err != nil {
			t.Fatalf("%s: %v", operation.name, err)
		}
	}

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":      []string{endpoint},
		"local_network":  "cn-east",
		"watch_networks": []string{"cn-north"},
		"auth": map[string]any{
			"username": "origin-east",
			"password": "origin-password",
		},
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "rbac-1",
		SessionID: 650,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	startProviderForTest(t, instance)
	if err := instance.Publish(
		context.Background(),
		recordTestNode("rbac-1", 650),
	); err != nil {
		t.Fatalf("local-network Publish() error = %v", err)
	}

	restricted, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
		Username:    "origin-east",
		Password:    "origin-password",
		Logger:      zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("restricted client error = %v", err)
	}
	defer restricted.Close()
	requestCtx, cancelRequest := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	if _, err := restricted.Get(
		requestCtx,
		"/origin/v1/networks/cn-north/nodes/",
		clientv3.WithPrefix(),
	); err != nil {
		cancelRequest()
		t.Fatalf("watched-network Range() error = %v", err)
	}
	if _, err := restricted.Put(
		requestCtx,
		"/origin/v1/networks/cn-north/nodes/forbidden",
		"forbidden",
	); err == nil {
		cancelRequest()
		t.Fatal("watched-network write unexpectedly succeeded")
	}
	cancelRequest()
	if err := instance.Withdraw(context.Background()); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if err := instance.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestEtcdProviderHTTPSVerificationModes(t *testing.T) {
	clientURL := reserveEtcdURLWithScheme(t, "https")
	peerURL := reserveEtcdURL(t)
	config := embed.NewConfig()
	config.Dir = t.TempDir()
	config.LogLevel = "error"
	config.LogOutputs = []string{os.DevNull}
	config.ListenClientUrls = []url.URL{clientURL}
	config.AdvertiseClientUrls = []url.URL{clientURL}
	config.ListenPeerUrls = []url.URL{peerURL}
	config.AdvertisePeerUrls = []url.URL{peerURL}
	config.ClientAutoTLS = true
	config.InitialCluster = config.InitialClusterFromName(config.Name)
	server := startConfiguredEtcd(t, config)
	defer server.Close()

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":     []string{clientURL.String()},
		"local_network": "cn-east",
		"tls": map[string]any{
			"insecure_skip_verify": true,
		},
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "tls-1",
		SessionID: 700,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("TLS Factory() error = %v", err)
	}
	startProviderForTest(t, instance)
	if err := instance.Close(context.Background()); err != nil {
		t.Fatalf("TLS Close() error = %v", err)
	}

	serverCertificate := filepath.Join(
		config.Dir,
		"fixtures",
		"client",
		"cert.pem",
	)
	verifiedConfig, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":     []string{clientURL.String()},
		"local_network": "cn-east",
		"tls": map[string]any{
			"ca_file":   serverCertificate,
			"cert_file": serverCertificate,
			"key_file": filepath.Join(
				config.Dir,
				"fixtures",
				"client",
				"key.pem",
			),
		},
	})
	verifiedRecorder := newIntegrationRecorder()
	verified, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "tls-verified",
		SessionID: 701,
		Config:    verifiedConfig,
		Host:      verifiedRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("verified TLS Factory() error = %v", err)
	}
	startProviderForTest(t, verified)
	if err := verified.Close(context.Background()); err != nil {
		t.Fatalf("verified TLS Close() error = %v", err)
	}
}

func TestEtcdProviderRealServerLifecycleLeaseAndContract(t *testing.T) {
	endpoint := startEmbeddedEtcd(t)
	raw, err := publicprovider.NewConfig(map[string]any{
		"endpoints": []string{
			"http://127.0.0.1:1",
			endpoint,
		},
		"local_network":   "cn-east",
		"ttl":             "3s",
		"dial_timeout":    "3s",
		"request_timeout": "3s",
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	factory := NewFactory(t.TempDir())

	publisherRecorder := newIntegrationRecorder()
	publisher, err := factory(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 11,
		Config:    raw,
		Host:      publisherRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("publisher Factory() error = %v", err)
	}
	startProviderForTest(t, publisher)

	observerRecorder := newIntegrationRecorder()
	observer, err := factory(publicprovider.Context{
		NodeID:    "observer-1",
		SessionID: 22,
		Config:    raw,
		Host:      observerRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("observer Factory() error = %v", err)
	}
	startProviderForTest(t, observer)
	t.Cleanup(func() { _ = observer.Close(context.Background()) })

	record := recordTestNode("game-1", 11)
	if err := publisher.Publish(context.Background(), record); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	observerRecorder.awaitNode(t, "game-1", true, 5*time.Second)
	if err := publisher.Publish(context.Background(), record); err != nil {
		t.Fatalf("idempotent Publish() error = %v", err)
	}

	duplicateRecorder := newIntegrationRecorder()
	duplicate, err := factory(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 33,
		Config:    raw,
		Host:      duplicateRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("duplicate Factory() error = %v", err)
	}
	startProviderForTest(t, duplicate)
	t.Cleanup(func() { _ = duplicate.Close(context.Background()) })
	duplicateRecord := recordTestNode("game-1", 33)
	if err := duplicate.Publish(context.Background(), duplicateRecord); !errs.IsCode(
		err,
		errs.CodeDiscoveryDuplicateNode,
	) {
		t.Fatalf("duplicate Publish() error = %v", err)
	}

	// Close deliberately skips Withdraw; the attached Lease must remove the key.
	if err := publisher.Close(context.Background()); err != nil {
		t.Fatalf("publisher Close() error = %v", err)
	}
	observerRecorder.awaitNode(t, "game-1", false, 8*time.Second)
	if err := duplicate.Publish(context.Background(), duplicateRecord); err != nil {
		t.Fatalf("post-expiry takeover Publish() error = %v", err)
	}
	observerRecorder.awaitNode(t, "game-1", true, 5*time.Second)
	if err := duplicate.Withdraw(context.Background()); err != nil {
		t.Fatalf("duplicate Withdraw() error = %v", err)
	}
	observerRecorder.awaitNode(t, "game-1", false, 5*time.Second)

	providertest.Run(t, providertest.Harness{
		Factory: factory,
		Config:  raw,
		Timeout: 8 * time.Second,
	})
}

func TestEtcdProviderExternalServerCompatibility(t *testing.T) {
	endpoint := os.Getenv("ORIGIN_ETCD_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("ORIGIN_ETCD_TEST_ENDPOINT is not configured")
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 3 * time.Second,
		Logger:      zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("compatibility client error = %v", err)
	}
	statusCtx, cancelStatus := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	status, err := client.Status(statusCtx, endpoint)
	cancelStatus()
	_ = client.Close()
	if err != nil {
		t.Fatalf("external etcd Status() error = %v", err)
	}
	t.Logf("external etcd server version: %s", status.Version)

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":       []string{endpoint},
		"namespace":       "/origin-compat",
		"local_network":   "cn-east",
		"watch_networks":  []string{"cn-north"},
		"ttl":             "3s",
		"dial_timeout":    "3s",
		"request_timeout": "3s",
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "compat-1",
		SessionID: 36014,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	startProviderForTest(t, instance)
	record := recordTestNode("compat-1", 36014)
	if err := instance.Publish(context.Background(), record); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	recorder.awaitNode(t, "compat-1", true, 5*time.Second)
	if err := instance.Withdraw(context.Background()); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	recorder.awaitNode(t, "compat-1", false, 5*time.Second)
	if err := instance.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestEtcdProviderExternalClusterRecovery 使用显式启用的真实三节点集群固定完整断线、
// 恢复和待提交 Publish 重放语义。测试日志中的两个标记供外部编排器确定停、启容器的时机；
// 普通测试不操作部署环境，也不会因未配置外部集群而变慢。
func TestEtcdProviderExternalClusterRecovery(t *testing.T) {
	if os.Getenv("ORIGIN_ETCD_RECOVERY_TEST") != "1" {
		t.Skip("设置 ORIGIN_ETCD_RECOVERY_TEST=1 后执行外部故障恢复测试")
	}
	rawEndpoints := strings.TrimSpace(os.Getenv("ORIGIN_ETCD_TEST_ENDPOINTS"))
	if rawEndpoints == "" {
		t.Fatal("ORIGIN_ETCD_TEST_ENDPOINTS is not configured")
	}
	parts := strings.Split(rawEndpoints, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		if endpoint := strings.TrimSpace(part); endpoint != "" {
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) < 3 {
		t.Fatalf("ORIGIN_ETCD_TEST_ENDPOINTS 需要至少三个地址，实际 %d", len(endpoints))
	}

	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":       endpoints,
		"namespace":       "/origin-m22-recovery",
		"local_network":   "origin",
		"ttl":             "3s",
		"dial_timeout":    "2s",
		"request_timeout": "2s",
	})
	recorder := newIntegrationRecorder()
	instance, err := NewFactory(t.TempDir())(publicprovider.Context{
		NodeID:    "m22-recovery-1",
		SessionID: 22001,
		Config:    raw,
		Host:      recorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}
	t.Cleanup(func() { _ = instance.Close(context.Background()) })
	startProviderForTest(t, instance)

	initial := recordTestNode("m22-recovery-1", 22001)
	if err := instance.Publish(context.Background(), initial); err != nil {
		t.Fatalf("initial Publish() error = %v", err)
	}
	recorder.awaitNode(t, initial.NodeID, true, 10*time.Second)
	t.Logf("EXTERNAL_ETCD_RECOVERY_READY endpoints=%s", strings.Join(endpoints, ","))

	// 外部编排器在 READY 后停止全部三个成员；Provider 必须报告 Recovering，不能保持
	// 虚假的 Ready。编排器看到 RECOVERING 后恢复集群，下面的 Publish 必须在恢复后完成。
	recorder.awaitState(t, publicprovider.StateRecovering, 30*time.Second)
	t.Log("EXTERNAL_ETCD_RECOVERING")
	updated := recordTestNode("m22-recovery-1", 22001)
	updated.Labels["revision"] = "recovered"
	publishResult := make(chan error, 1)
	go func() {
		publishCtx, cancelPublish := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelPublish()
		publishResult <- instance.Publish(publishCtx, updated)
	}()
	recorder.awaitState(t, publicprovider.StateReady, 60*time.Second)
	if err := <-publishResult; err != nil {
		t.Fatalf("recovery Publish() error = %v", err)
	}
	recorder.awaitNode(t, updated.NodeID, true, 10*time.Second)
	if err := instance.Withdraw(context.Background()); err != nil {
		t.Fatalf("recovered Withdraw() error = %v", err)
	}
	recorder.awaitNode(t, updated.NodeID, false, 10*time.Second)
}

func startProviderForTest(t *testing.T, provider publicprovider.Provider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("Provider Start() error = %v", err)
	}
}

func startEmbeddedEtcd(t *testing.T) string {
	t.Helper()
	clientURL := reserveEtcdURL(t)
	peerURL := reserveEtcdURL(t)
	config := embed.NewConfig()
	config.Dir = t.TempDir()
	config.LogLevel = "error"
	config.LogOutputs = []string{os.DevNull}
	config.ListenClientUrls = []url.URL{clientURL}
	config.AdvertiseClientUrls = []url.URL{clientURL}
	config.ListenPeerUrls = []url.URL{peerURL}
	config.AdvertisePeerUrls = []url.URL{peerURL}
	config.InitialCluster = config.InitialClusterFromName(config.Name)
	server := startConfiguredEtcd(t, config)
	t.Cleanup(server.Close)
	return clientURL.String()
}

func startConfiguredEtcd(t *testing.T, config *embed.Config) *embed.Etcd {
	t.Helper()
	server, err := embed.StartEtcd(config)
	if err != nil {
		t.Fatalf("StartEtcd() error = %v", err)
	}
	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		server.Server.Stop()
		t.Fatal("embedded etcd 启动超时")
	}
	return server
}

func reserveEtcdURL(t *testing.T) url.URL {
	return reserveEtcdURLWithScheme(t, "http")
}

func reserveEtcdURLWithScheme(t *testing.T, scheme string) url.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve etcd address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved address: %v", err)
	}
	parsed, err := url.Parse(scheme + "://" + address)
	if err != nil {
		t.Fatalf("parse reserved URL: %v", err)
	}
	return *parsed
}
