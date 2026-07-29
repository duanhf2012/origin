package rpcfixture

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
)

const (
	processHelperEnabled = "ORIGIN_M13_PROCESS_HELPER"
	processHelperAddress = "ORIGIN_M13_PROCESS_ADDRESS"
	processReadyLine     = "ORIGIN_M13_READY"
)

// TestRemoteRPCIndependentProcesses 验证调用 Node 和目标 Node 不共享 Go 指针或 Runtime。
func TestRemoteRPCIndependentProcesses(t *testing.T) {
	targetConfig := testRPCConfig(t)
	callerConfig := testRPCConfig(t)
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestRemoteRPCProcessHelper$",
		"-test.v",
	)
	command.Env = append(
		os.Environ(),
		processHelperEnabled+"=1",
		processHelperAddress+"="+targetConfig.TCP.Advertise,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan error, 1)
	go func() {
		childDone <- command.Wait()
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case err := <-childDone:
			if err != nil {
				t.Errorf("目标测试进程退出失败: %v\n%s", err, stderr.String())
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			t.Errorf("目标测试进程没有按时退出\n%s", stderr.String())
		}
	})

	// 子进程只有在真实 Listener 已经绑定后才输出就绪标记。
	scanner := bufio.NewScanner(stdout)
	ready := false
	var targetSessionID uint64
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == processReadyLine {
			ready = true
			targetSessionID, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				t.Fatalf("解析子进程 SessionID: %v", err)
			}
			break
		}
	}
	if !ready {
		t.Fatalf(
			"目标测试进程未就绪: scan=%v stderr=%s",
			scanner.Err(),
			stderr.String(),
		)
	}
	// 测试框架结束时还会输出 PASS；继续排空，避免子进程 stdout 管道回压。
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
	}()

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	caller := &CallerService{}
	discoverySource := internaldiscovery.NewSource()
	if err := discoverySource.Publish(internaldiscovery.RawNode{
		NodeID:    "player-1",
		SessionID: targetSessionID,
		Transport: internaldiscovery.TransportTCP,
		Address:   targetConfig.TCP.Advertise,
		Services: []internaldiscovery.RawService{{
			ServiceName:         "PlayerService",
			State:               internaldiscovery.ServiceStateRunning,
			ContractID:          uint64(playerRPCContractID),
			ContractFingerprint: [32]byte(playerRPCFingerprint),
		}},
	}); err != nil {
		t.Fatalf("发布子进程发现记录: %v", err)
	}
	callerNode := newRemoteFixtureNode(
		t,
		"gateway-process",
		callerConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	if err := callerNode.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopTestNode(t, callerNode)
	})
	fixture := &remoteRPCFixture{
		callerNode: callerNode,
		caller:     caller,
		pool:       pool,
	}
	if result := awaitRemoteEcho(t, fixture, "process"); result != "process-echo" {
		t.Fatalf("independent process AwaitEchoName() = %q", result)
	}
}

// TestRemoteRPCProcessHelper 只在父测试显式设置环境变量时作为独立目标进程运行。
func TestRemoteRPCProcessHelper(t *testing.T) {
	if os.Getenv(processHelperEnabled) != "1" {
		return
	}
	address := os.Getenv(processHelperAddress)
	config := rpc.DefaultConfig()
	config.TCP.Listen = address
	config.TCP.Advertise = address
	config.TCP.ReadIdleTimeout = time.Second
	config.TCP.WriteTimeout = time.Second
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	player := &PlayerService{}
	discoverySource := internaldiscovery.NewSource()
	published := make(chan internaldiscovery.RawNode, 1)
	subscription, err := discoverySource.Subscribe(
		func(snapshot internaldiscovery.RawSnapshot) error {
			for _, record := range snapshot.Nodes {
				if record.NodeID == "player-1" && len(record.Services) != 0 {
					select {
					case published <- record:
					default:
					}
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("订阅子进程发现源: %v", err)
	}
	defer subscription.Close()
	target := newRemoteFixtureNode(
		t,
		"player-1",
		config,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	if err := target.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := <-published
	fmt.Printf("%s %d\n", processReadyLine, record.SessionID)

	// 父进程关闭 stdin 表示调用验证完成；轮询文件和额外控制端口都不需要。
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatalf("等待父进程关闭信号: %v", err)
	}
	stopTestNode(t, target)
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("子进程 Buffer 未归还: %+v", stats)
	}
}
