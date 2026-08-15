package compose_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/duanhf2012/origin/v3/rpc"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

func TestNATSMaxPayloadCoversOriginMessages(t *testing.T) {
	options, err := natsserver.ProcessConfigFile(filepath.Join(".", "nats.conf"))
	if err != nil {
		t.Fatalf("parse nats.conf: %v", err)
	}
	// 额外 1K 覆盖当前最坏 549B RPC 包络，并避免部署配置只等于业务 payload。
	want := int32(rpc.DefaultMaxPayloadSize + 1024)
	if options.MaxPayload < want {
		t.Fatalf(
			"nats max_payload = %d, want at least %d",
			options.MaxPayload,
			want,
		)
	}
}

// TestComposePublishedPortsDefaultToLoopback 固定本地依赖的安全默认值。需要跨主机联调时，
// 使用者必须显式设置 ORIGIN_BIND_ADDRESS，并自行配置认证与网络访问控制。
func TestComposePublishedPortsDefaultToLoopback(t *testing.T) {
	portMapping := regexp.MustCompile(`(?m)^\s*-\s*"(.+):[0-9]+:[0-9]+"\r?$`)
	for name, wantMappings := range map[string]int{
		"base-compose.yml": 4,
	} {
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		mappings := portMapping.FindAllStringSubmatch(string(data), -1)
		if len(mappings) != wantMappings {
			t.Errorf("%s published mappings = %d, want %d", name, len(mappings), wantMappings)
		}
		for _, match := range mappings {
			if match[1] != "${ORIGIN_BIND_ADDRESS:-127.0.0.1}" {
				t.Errorf("%s published host = %q, want explicit loopback default", name, match[1])
			}
		}
	}
}
