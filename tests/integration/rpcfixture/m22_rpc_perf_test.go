package rpcfixture

import (
	"testing"
	"time"
)

type m22RPCPerfTransport string

const (
	m22RPCPerfLocal m22RPCPerfTransport = "local"
	m22RPCPerfTCP   m22RPCPerfTransport = "tcp"
	m22RPCPerfNATS  m22RPCPerfTransport = "nats"
)

type m22RPCPerfMethod string

const (
	m22RPCPerfAsync m22RPCPerfMethod = "async"
	m22RPCPerfAwait m22RPCPerfMethod = "await"
)

type m22RPCPerfMode string

const (
	m22RPCPerfLatency    m22RPCPerfMode = "latency"
	m22RPCPerfThroughput m22RPCPerfMode = "throughput"
)

type m22RPCPerfCase struct {
	Transport    m22RPCPerfTransport `json:"transport"`
	Method       m22RPCPerfMethod    `json:"method"`
	Mode         m22RPCPerfMode      `json:"mode"`
	PayloadBytes int                 `json:"payload_bytes"`
	Concurrency  int                 `json:"concurrency"`
}

type m22RPCPerfRunProfile struct {
	Warmup  time.Duration
	Measure time.Duration
	Rounds  int
}

func m22RPCPerfProfile(short bool) m22RPCPerfRunProfile {
	if short {
		return m22RPCPerfRunProfile{
			Warmup:  20 * time.Millisecond,
			Measure: 50 * time.Millisecond,
			Rounds:  1,
		}
	}
	return m22RPCPerfRunProfile{
		Warmup:  5 * time.Second,
		Measure: 15 * time.Second,
		Rounds:  3,
	}
}

func m22RPCPerfCases() []m22RPCPerfCase {
	transports := [...]m22RPCPerfTransport{
		m22RPCPerfLocal,
		m22RPCPerfTCP,
		m22RPCPerfNATS,
	}
	methods := [...]m22RPCPerfMethod{m22RPCPerfAsync, m22RPCPerfAwait}
	loads := [...]struct {
		mode        m22RPCPerfMode
		payload     int
		concurrency int
	}{
		{mode: m22RPCPerfLatency, payload: 32, concurrency: 1},
		{mode: m22RPCPerfThroughput, payload: 32, concurrency: 64},
		{mode: m22RPCPerfThroughput, payload: 1024, concurrency: 32},
		{mode: m22RPCPerfThroughput, payload: 64 * 1024, concurrency: 32},
	}
	result := make([]m22RPCPerfCase, 0, len(transports)*len(methods)*len(loads))
	for _, transport := range transports {
		for _, method := range methods {
			for _, load := range loads {
				result = append(result, m22RPCPerfCase{
					Transport:    transport,
					Method:       method,
					Mode:         load.mode,
					PayloadBytes: load.payload,
					Concurrency:  load.concurrency,
				})
			}
		}
	}
	return result
}

// TestM22RPCPerfMatrixDefinition 固定发布门禁必须枚举的全部路径和负载，防止长压测
// 因新增分支或重构静默漏掉 Transport、调用方式或 32B 的独立吞吐档。
func TestM22RPCPerfMatrixDefinition(t *testing.T) {
	cases := m22RPCPerfCases()
	if len(cases) != 24 {
		t.Fatalf("M22 RPC performance cases = %d, want 24", len(cases))
	}
	seen := make(map[m22RPCPerfCase]struct{}, len(cases))
	for _, current := range cases {
		if _, duplicate := seen[current]; duplicate {
			t.Fatalf("duplicate M22 RPC performance case: %+v", current)
		}
		seen[current] = struct{}{}
	}
	for _, transport := range []m22RPCPerfTransport{
		m22RPCPerfLocal,
		m22RPCPerfTCP,
		m22RPCPerfNATS,
	} {
		for _, method := range []m22RPCPerfMethod{
			m22RPCPerfAsync,
			m22RPCPerfAwait,
		} {
			for _, load := range []struct {
				payload     int
				concurrency int
				mode        m22RPCPerfMode
			}{
				{payload: 32, concurrency: 1, mode: m22RPCPerfLatency},
				{payload: 32, concurrency: 64, mode: m22RPCPerfThroughput},
				{payload: 1024, concurrency: 32, mode: m22RPCPerfThroughput},
				{payload: 64 * 1024, concurrency: 32, mode: m22RPCPerfThroughput},
			} {
				key := m22RPCPerfCase{
					Transport:    transport,
					Method:       method,
					Mode:         load.mode,
					PayloadBytes: load.payload,
					Concurrency:  load.concurrency,
				}
				if _, exists := seen[key]; !exists {
					t.Errorf("missing M22 RPC performance case: %+v", key)
				}
			}
		}
	}
}

// TestM22RPCPerfProfiles 固定正式口径，并让显式短模式能够在开发机验证全部运行分支。
func TestM22RPCPerfProfiles(t *testing.T) {
	full := m22RPCPerfProfile(false)
	if full.Warmup != 5*time.Second || full.Measure != 15*time.Second ||
		full.Rounds != 3 {
		t.Fatalf("full M22 RPC performance profile = %+v", full)
	}
	short := m22RPCPerfProfile(true)
	if short.Warmup <= 0 || short.Measure <= 0 || short.Rounds != 1 ||
		short.Warmup >= full.Warmup || short.Measure >= full.Measure {
		t.Fatalf("short M22 RPC performance profile = %+v", short)
	}
}
