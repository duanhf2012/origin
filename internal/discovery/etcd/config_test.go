package etcd

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
)

func TestDecodeConfigDefaultsNormalizesNetworksAndEndpoints(t *testing.T) {
	raw, err := publicprovider.NewConfig(map[string]any{
		"endpoints": []string{
			"HTTP://ETCD-1.EXAMPLE.COM:2379",
			"http://etcd-1.example.com:2379",
			"http://etcd-2.example.com:2379",
		},
		"local_network":  "cn-east",
		"watch_networks": []string{"cn-north", "cn-east", "cn-west"},
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	config, err := DecodeConfig(raw, t.TempDir())
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if config.Namespace != "/origin" || config.TTL != 15*time.Second ||
		config.DialTimeout != 5*time.Second ||
		config.RequestTimeout != 5*time.Second {
		t.Fatalf("defaults = %+v", config)
	}
	if !slices.Equal(config.Endpoints, []string{
		"http://etcd-1.example.com:2379",
		"http://etcd-2.example.com:2379",
	}) {
		t.Fatalf("endpoints = %v", config.Endpoints)
	}
	if !slices.Equal(config.Networks, []string{
		"cn-east",
		"cn-north",
		"cn-west",
	}) {
		t.Fatalf("networks = %v", config.Networks)
	}
	if !slices.Equal(config.WatchNetworks, []string{"cn-north", "cn-west"}) {
		t.Fatalf("watch networks = %v", config.WatchNetworks)
	}
}

func TestDecodeConfigRejectsInvalidCombinations(t *testing.T) {
	tooManyNetworks := make([]string, 64)
	for index := range tooManyNetworks {
		tooManyNetworks[index] = fmt.Sprintf("network-%d", index)
	}
	tests := []map[string]any{
		{"local_network": "cn-east"},
		{
			"endpoints":     []string{"etcd.example.com:2379"},
			"local_network": "cn-east",
		},
		{
			"endpoints": []string{
				"http://etcd.example.com:2379",
				"https://etcd.example.com:2379",
			},
			"local_network": "cn-east",
		},
		{
			"endpoints":     []string{"http://user@etcd.example.com:2379"},
			"local_network": "cn-east",
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379/path"},
			"local_network": "cn-east",
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"namespace":     "/",
			"local_network": "cn-east",
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "CN-East",
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "cn-east",
			"ttl":           "3500ms",
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "cn-east",
			"auth": map[string]any{
				"username": "origin",
			},
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "cn-east",
			"auth": map[string]any{
				"username": "origin",
				"password": "secret",
				"token":    "token",
			},
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "cn-east",
			"tls": map[string]any{
				"server_name": "etcd.example.com",
			},
		},
		{
			"endpoints":     []string{"https://etcd.example.com:2379"},
			"local_network": "cn-east",
			"tls": map[string]any{
				"cert_file": "client.pem",
			},
		},
		{
			"endpoints":      []string{"http://etcd.example.com:2379"},
			"local_network":  "cn-east",
			"watch_networks": tooManyNetworks,
		},
		{
			"endpoints":     []string{"http://etcd.example.com:2379"},
			"local_network": "cn-east",
			"unknown":       true,
		},
	}
	for index, value := range tests {
		raw, _ := publicprovider.NewConfig(value)
		if _, err := DecodeConfig(raw, t.TempDir()); !errs.IsCode(
			err,
			errs.CodeInvalidConfig,
		) {
			t.Errorf("case %d DecodeConfig() error = %v", index, err)
		}
	}
}

func TestDecodeConfigResolvesTLSFilesFromConfigRoot(t *testing.T) {
	root := t.TempDir()
	raw, _ := publicprovider.NewConfig(map[string]any{
		"endpoints":     []string{"https://etcd.example.com:2379"},
		"local_network": "cn-east",
		"tls": map[string]any{
			"ca_file": "certs/ca.pem",
		},
	})
	_, err := DecodeConfig(raw, root)
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("missing relative CA error = %v", err)
	}
	expected := filepath.Join(root, "certs", "ca.pem")
	config := TLSConfig{Enabled: true, CAFile: expected}
	if _, err := config.load(); err == nil {
		t.Fatal("resolved missing CA unexpectedly loaded")
	}
}
