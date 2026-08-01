package etcd

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	watchEventCapacity = 256
	progressInterval   = 30 * time.Second
	progressTimeout    = 90 * time.Second
)

type commandKind uint8

const (
	commandPublish commandKind = iota + 1
	commandWithdraw
)

type providerCommand struct {
	kind   commandKind
	ctx    context.Context
	node   publicprovider.Node
	result chan error
}

// Provider owns one official etcd Client, all network watches, and this Node's lease.
type Provider struct {
	context publicprovider.Context
	config  Config

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}

	commands chan providerCommand
	startedC chan error
}

// NewFactory captures the Application configuration root without expanding the public SPI.
func NewFactory(configRoot string) publicprovider.Factory {
	return func(context publicprovider.Context) (publicprovider.Provider, error) {
		config, err := DecodeConfig(context.Config, configRoot)
		if err != nil {
			return nil, err
		}
		if config.TLS.InsecureSkipVerify {
			context.Logger.Warn(
				"etcd TLS certificate verification is explicitly disabled",
			)
		}
		return &Provider{
			context:  context,
			config:   config,
			commands: make(chan providerCommand),
			startedC: make(chan error, 1),
			done:     make(chan struct{}),
		}, nil
	}
}

// Start connects, ranges every selected network, establishes watches, and submits the first snapshot.
func (provider *Provider) Start(ctx context.Context) error {
	if provider == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	provider.mu.Lock()
	if provider.started || provider.closed {
		provider.mu.Unlock()
		return errs.ErrInvalidArgument
	}
	runCtx, cancel := context.WithCancel(context.Background())
	provider.cancel = cancel
	provider.started = true
	provider.mu.Unlock()

	if err := provider.context.Host.SetTTL(provider.config.TTL); err != nil {
		cancel()
		close(provider.done)
		return err
	}
	provider.context.Host.Report(publicprovider.Report{
		State: publicprovider.StateStarting,
	})
	go provider.ownerLoop(runCtx)

	select {
	case err := <-provider.startedC:
		if err != nil {
			cancel()
			<-provider.done
		}
		return err
	case <-ctx.Done():
		cancel()
		<-provider.done
		return wrapContext(ctx.Err())
	}
}

// Publish atomically creates or updates this Node's full record under its lease.
func (provider *Provider) Publish(
	ctx context.Context,
	node publicprovider.Node,
) error {
	if provider == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	normalized, err := publicprovider.NormalizeNode(node)
	if err != nil {
		return err
	}
	if normalized.NodeID != provider.context.NodeID ||
		normalized.SessionID != provider.context.SessionID {
		return errs.NewMessage(
			errs.CodeDiscoverySnapshotInvalid,
			"etcd Publish Node 身份与 Provider Context 不一致",
		)
	}
	return provider.command(ctx, providerCommand{
		kind:   commandPublish,
		node:   normalized,
		result: make(chan error, 1),
	})
}

// Withdraw CAS-deletes only the record still owned by this exact Session.
func (provider *Provider) Withdraw(ctx context.Context) error {
	if provider == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	return provider.command(ctx, providerCommand{
		kind:   commandWithdraw,
		result: make(chan error, 1),
	})
}

func (provider *Provider) command(
	ctx context.Context,
	command providerCommand,
) error {
	provider.mu.Lock()
	started := provider.started
	closed := provider.closed
	done := provider.done
	provider.mu.Unlock()
	if !started || closed {
		return errs.ErrServiceStopped
	}
	command.ctx = ctx
	select {
	case provider.commands <- command:
	case <-ctx.Done():
		return wrapContext(ctx.Err())
	case <-done:
		return errs.ErrServiceStopped
	}
	select {
	case err := <-command.result:
		return err
	case <-ctx.Done():
		return wrapContext(ctx.Err())
	case <-done:
		return errs.ErrServiceStopped
	}
}

// Close cancels all operations, waits for watches and KeepAlive, then closes the Client.
func (provider *Provider) Close(ctx context.Context) error {
	if provider == nil {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	provider.mu.Lock()
	if provider.closed {
		done := provider.done
		started := provider.started
		provider.mu.Unlock()
		if !started {
			return nil
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return wrapContext(ctx.Err())
		}
	}
	provider.closed = true
	cancel := provider.cancel
	started := provider.started
	provider.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		close(provider.done)
		return nil
	}
	select {
	case <-provider.done:
		return nil
	case <-ctx.Done():
		return wrapContext(ctx.Err())
	}
}

func (provider *Provider) ownerLoop(ctx context.Context) {
	defer close(provider.done)
	var (
		desired       *publicprovider.Node
		confirmed     *publicprovider.Node
		pending       *providerCommand
		clusterID     uint64
		reconnects    uint64
		failures      uint32
		startComplete bool
		backoff       = 100 * time.Millisecond
		randomState   = provider.context.SessionID ^ 0xd1b54a32d192ed03
	)
	defer func() {
		if pending != nil {
			pending.result <- errs.ErrServiceStopped
		}
	}()
	defer provider.context.Host.Report(publicprovider.Report{
		State: publicprovider.StateStopped,
	})

	finishStart := func(err error) {
		select {
		case provider.startedC <- err:
		default:
		}
	}
	reportRecovering := func(code errs.Code) {
		failures++
		provider.context.Host.Report(publicprovider.Report{
			State:               publicprovider.StateRecovering,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
			ErrorCode:           code,
		})
	}
	reportReady := func() {
		provider.context.Host.Report(publicprovider.Report{
			State:               publicprovider.StateReady,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
		})
	}

	for {
		current, err := provider.openSession(ctx, clusterID)
		needsWatchSync := false
		if err == nil {
			if clusterID == 0 {
				clusterID = current.clusterID
			}
			if desired != nil {
				err = current.publish(ctx, *desired)
				needsWatchSync = err == nil
			} else if pending != nil && pending.kind == commandWithdraw {
				err = current.withdraw(ctx)
				needsWatchSync = err == nil
			}
			if err == nil {
				confirmed = cloneNodePointer(desired)
			} else if pending != nil && deterministic(err) {
				pending.result <- err
				pending = nil
				desired = cloneNodePointer(confirmed)
				if desired == nil {
					err = nil
				} else {
					err = current.publish(ctx, *desired)
					needsWatchSync = err == nil
				}
			}
		}
		if err == nil && needsWatchSync {
			err = current.syncWatches(ctx, false)
		}
		if err == nil {
			snapshot, snapshotErr := current.snapshot()
			if snapshotErr == nil {
				snapshotErr = provider.context.Host.ReplaceSnapshot(snapshot)
			}
			err = snapshotErr
		}
		if err != nil {
			if current != nil {
				current.close()
			}
			code := recoveryCode(err)
			reportRecovering(code)
			if !startComplete && deterministic(err) {
				finishStart(err)
				return
			}
			if !provider.waitRecovery(
				ctx,
				time.Now().Add(jitterDelay(backoff, 20, &randomState)),
				&desired,
				&pending,
			) {
				if !startComplete {
					finishStart(wrapContext(ctx.Err()))
				}
				return
			}
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			continue
		}

		if startComplete {
			reconnects++
		}
		reportReady()
		if pending != nil {
			pending.result <- nil
			pending = nil
		}
		if !startComplete {
			startComplete = true
			finishStart(nil)
		}
		stableSince := time.Now()
		recoverSession := false

		for !recoverSession {
			select {
			case <-ctx.Done():
				current.close()
				return
			case command := <-provider.commands:
				switch command.kind {
				case commandPublish:
					err = current.publish(command.ctx, command.node)
					if err == nil {
						desired = cloneNodePointer(&command.node)
						confirmed = cloneNodePointer(&command.node)
					} else if !deterministic(err) {
						desired = cloneNodePointer(&command.node)
						commandCopy := command
						pending = &commandCopy
						recoverSession = true
					} else {
						desired = cloneNodePointer(confirmed)
					}
				case commandWithdraw:
					desired = nil
					err = current.withdraw(command.ctx)
					if err == nil {
						confirmed = nil
					} else if !deterministic(err) {
						commandCopy := command
						pending = &commandCopy
						recoverSession = true
					} else {
						desired = cloneNodePointer(confirmed)
					}
				default:
					err = errs.ErrInvalidArgument
				}
				if pending == nil {
					command.result <- err
				}
			case envelope := <-current.events:
				snapshots, eventErr := current.ingest(envelope)
				if eventErr == nil {
					for _, snapshot := range snapshots {
						if eventErr = provider.context.Host.ReplaceSnapshot(snapshot); eventErr != nil {
							break
						}
					}
				}
				if eventErr != nil {
					err = eventErr
					recoverSession = true
					continue
				}
				if time.Since(stableSince) >= provider.config.TTL {
					failures = 0
					backoff = 100 * time.Millisecond
				}
				reportReady()
			case response, open := <-current.keepAlive:
				if !open || response == nil || response.TTL <= 0 ||
					response.ID != current.leaseID ||
					current.checkHeader(response.ResponseHeader) != nil {
					err = errs.ErrDiscoveryUnavailable
					recoverSession = true
					continue
				}
				if time.Since(stableSince) >= provider.config.TTL {
					failures = 0
					backoff = 100 * time.Millisecond
				}
				reportReady()
			case <-current.progress.C:
				if current.progressExpired() {
					err = errs.ErrDiscoveryUnavailable
					recoverSession = true
					continue
				}
				progressCtx := clientv3.WithRequireLeader(
					current.watchCtx,
				)
				requestCtx, cancel := context.WithTimeout(
					progressCtx,
					provider.config.RequestTimeout,
				)
				err = current.client.RequestProgress(requestCtx)
				cancel()
				if err != nil {
					recoverSession = true
					continue
				}
				current.progress.Reset(progressInterval)
			}
		}
		current.close()
		reportRecovering(recoveryCode(err))
		if !provider.waitRecovery(
			ctx,
			time.Now().Add(jitterDelay(backoff, 20, &randomState)),
			&desired,
			&pending,
		) {
			return
		}
		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
}

func (provider *Provider) waitRecovery(
	ctx context.Context,
	deadline time.Time,
	desired **publicprovider.Node,
	pending **providerCommand,
) bool {
	for {
		wait := time.Until(deadline)
		if wait <= 0 {
			return true
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			return true
		case command := <-provider.commands:
			stopTimer(timer)
			if *pending != nil {
				(*pending).result <- errs.ErrDiscoveryUnavailable
			}
			switch command.kind {
			case commandPublish:
				*desired = cloneNodePointer(&command.node)
			case commandWithdraw:
				*desired = nil
			}
			commandCopy := command
			*pending = &commandCopy
		case <-ctx.Done():
			stopTimer(timer)
			return false
		}
	}
}

func (provider *Provider) newClient(ctx context.Context) (*clientv3.Client, error) {
	tlsConfig, err := provider.config.TLS.load()
	if err != nil {
		return nil, ConfigError(err)
	}
	return clientv3.New(clientv3.Config{
		Endpoints:            append([]string(nil), provider.config.Endpoints...),
		DialTimeout:          provider.config.DialTimeout,
		DialKeepAliveTime:    30 * time.Second,
		DialKeepAliveTimeout: 10 * time.Second,
		MaxCallSendMsgSize:   publicprovider.MaxRecordSize + 64*1024,
		MaxCallRecvMsgSize:   publicprovider.MaxSnapshotSize + 1024*1024,
		TLS:                  tlsConfig,
		Username:             provider.config.Auth.Username,
		Password:             provider.config.Auth.Password,
		Token:                provider.config.Auth.Token,
		Context:              ctx,
		Logger:               zap.NewNop(),
		// 保持 PermitWithoutStream 的默认 false：Watch 与 Lease 已为活动流提供保活，空闲
		// endpoint 若继续无数据 ping，会触发 etcd 的 gRPC too_many_pings 连接驱逐。
		PermitWithoutStream: false,
	})
}

func cloneNodePointer(input *publicprovider.Node) *publicprovider.Node {
	if input == nil {
		return nil
	}
	node, err := publicprovider.NormalizeNode(*input)
	if err != nil {
		return nil
	}
	return &node
}

func deterministic(err error) bool {
	if err == nil {
		return false
	}
	switch errs.CodeOf(err) {
	case errs.CodeInvalidConfig,
		errs.CodeDiscoveryDuplicateNode,
		errs.CodeDiscoveryCapacity,
		errs.CodeDiscoverySnapshotInvalid:
		return true
	}
	code := status.Code(err)
	return code == codes.Unauthenticated || code == codes.PermissionDenied
}

func recoveryCode(err error) errs.Code {
	if err == nil {
		return 0
	}
	switch code := errs.CodeOf(err); code {
	case errs.CodeDiscoveryCapacity, errs.CodeDiscoverySnapshotInvalid:
		return code
	default:
		return errs.CodeDiscoveryUnavailable
	}
}

func wrapContext(err error) error {
	if err == nil {
		return errs.ErrCanceled
	}
	return errs.Wrap(errs.CodeOf(err), err)
}

func jitterDelay(base time.Duration, percent int64, state *uint64) time.Duration {
	if base <= 0 || percent <= 0 || state == nil {
		return base
	}
	value := *state
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	*state = value
	span := int64(base) * percent / 100
	if span <= 0 {
		return base
	}
	offset := int64(value%(uint64(span)*2+1)) - span
	return base + time.Duration(offset)
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func nodesEqual(left, right publicprovider.Node) bool {
	return left.NodeID == right.NodeID &&
		left.SessionID == right.SessionID &&
		left.Transport == right.Transport &&
		left.Address == right.Address &&
		mapsEqual(left.Labels, right.Labels) &&
		slices.Equal(left.Services, right.Services)
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func operationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("etcd discovery %s: %w", operation, err)
}

var _ publicprovider.Provider = (*Provider)(nil)
