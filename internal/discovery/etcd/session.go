package etcd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var errClusterMismatch = errors.New("etcd discovery cluster ID mismatch")

type networkMirror struct {
	records      map[string]publicprovider.Node
	rangeRev     int64
	createdRev   int64
	observedRev  int64
	lastActivity time.Time
}

type watchEnvelope struct {
	network  string
	response clientv3.WatchResponse
	closed   bool
}

type watchMutation struct {
	network string
	event   *clientv3.Event
}

type providerSession struct {
	config        Config
	client        *clientv3.Client
	clusterID     uint64
	nodeID        string
	sessionID     uint64
	networks      map[string]*networkMirror
	events        chan watchEnvelope
	pending       map[int64][]watchMutation
	rangeNodes    int
	rangeServices int
	rangeBytes    int

	watchCtx    context.Context
	watchCancel context.CancelFunc
	watchWG     sync.WaitGroup
	progress    *time.Timer

	leaseID     clientv3.LeaseID
	leaseCancel context.CancelFunc
	keepAlive   <-chan *clientv3.LeaseKeepAliveResponse
}

func (provider *Provider) openSession(
	ctx context.Context,
	expectedClusterID uint64,
) (*providerSession, error) {
	client, err := provider.newClient(ctx)
	if err != nil {
		return nil, operationError("create client", err)
	}
	watchCtx, watchCancel := context.WithCancel(ctx)
	session := &providerSession{
		config:      provider.config,
		client:      client,
		clusterID:   expectedClusterID,
		nodeID:      provider.context.NodeID,
		sessionID:   provider.context.SessionID,
		networks:    make(map[string]*networkMirror, len(provider.config.Networks)),
		events:      make(chan watchEnvelope, watchEventCapacity),
		pending:     make(map[int64][]watchMutation),
		watchCtx:    watchCtx,
		watchCancel: watchCancel,
	}
	rangeRevision := int64(0)
	for _, network := range provider.config.Networks {
		mirror, rangeErr := session.rangeNetwork(ctx, network, rangeRevision)
		if rangeErr != nil {
			session.close()
			return nil, rangeErr
		}
		if rangeRevision == 0 {
			rangeRevision = mirror.rangeRev
		}
		session.networks[network] = mirror
	}
	if _, err := session.snapshot(); err != nil {
		session.close()
		return nil, err
	}

	created := make(map[string]bool, len(provider.config.Networks))
	for _, network := range provider.config.Networks {
		session.startWatch(network)
	}
	for len(created) != len(provider.config.Networks) {
		select {
		case <-ctx.Done():
			session.close()
			return nil, wrapContext(ctx.Err())
		case envelope := <-session.events:
			if envelope.closed {
				session.close()
				return nil, errs.ErrDiscoveryUnavailable
			}
			if envelope.response.Created {
				created[envelope.network] = true
			}
			if _, ingestErr := session.ingest(envelope); ingestErr != nil {
				session.close()
				return nil, ingestErr
			}
		}
	}
	if err := session.syncWatches(ctx, true); err != nil {
		session.close()
		return nil, err
	}
	session.progress = time.NewTimer(progressInterval)
	return session, nil
}

func (session *providerSession) rangeNetwork(
	ctx context.Context,
	network string,
	snapshotRevision int64,
) (*networkMirror, error) {
	prefix := session.networkPrefix(network)
	rangeEnd := clientv3.GetPrefixRangeEnd(prefix)
	start := prefix
	revision := snapshotRevision
	result := &networkMirror{
		records:      make(map[string]publicprovider.Node),
		lastActivity: time.Now(),
	}
	for {
		requestCtx, cancel := context.WithTimeout(ctx, session.config.RequestTimeout)
		options := []clientv3.OpOption{
			clientv3.WithRange(rangeEnd),
			clientv3.WithLimit(rangePageSize),
			clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
		}
		if revision != 0 {
			options = append(options, clientv3.WithRev(revision))
		}
		response, err := session.client.Get(requestCtx, start, options...)
		cancel()
		if err != nil {
			if errors.Is(err, rpctypes.ErrCompacted) {
				return nil, errs.ErrDiscoveryUnavailable
			}
			return nil, operationError("range", err)
		}
		if err := session.checkHeader(response.Header); err != nil {
			return nil, err
		}
		if revision == 0 {
			revision = response.Header.Revision
			if revision <= 0 {
				return nil, errs.ErrDiscoveryUnavailable
			}
		}
		for _, keyValue := range response.Kvs {
			nodeID, err := session.parseNodeKey(network, keyValue.Key)
			if err != nil {
				return nil, err
			}
			recordNetwork, node, err := decodeRecord(keyValue.Value)
			if err != nil {
				return nil, err
			}
			if recordNetwork != network || node.NodeID != nodeID {
				return nil, invalidRecord("Key 与 Value 身份不一致")
			}
			if _, duplicate := result.records[nodeID]; duplicate {
				return nil, invalidRecord("Range 返回重复 Node Key")
			}
			if err := session.reserveRangeRecord(
				node,
				len(keyValue.Value),
			); err != nil {
				return nil, err
			}
			result.records[nodeID] = node
		}
		if !response.More {
			break
		}
		if len(response.Kvs) == 0 {
			return nil, invalidRecord("Range More 未携带下一页 Key")
		}
		start = string(append(
			append([]byte(nil), response.Kvs[len(response.Kvs)-1].Key...),
			0,
		))
	}
	result.rangeRev = revision
	result.observedRev = revision
	return result, nil
}

func (session *providerSession) reserveRangeRecord(
	node publicprovider.Node,
	encodedSize int,
) error {
	nextNodes := session.rangeNodes + 1
	nextServices := session.rangeServices + len(node.Services)
	nextBytes := session.rangeBytes + encodedSize
	if nextNodes > publicprovider.MaxNodes ||
		nextServices > publicprovider.MaxServices ||
		nextBytes > publicprovider.MaxSnapshotSize {
		return errs.NewMessage(
			errs.CodeDiscoveryCapacity,
			"etcd 服务发现 Range 超过完整快照容量",
		)
	}
	session.rangeNodes = nextNodes
	session.rangeServices = nextServices
	session.rangeBytes = nextBytes
	return nil
}

// syncWatches waits for an explicit progress response from every prefix.
//
// A progress response is ordered after all historical events already queued on that watch.
// Waiting for every prefix therefore closes the Range/Watch gap and also lets transactions that
// touch multiple watched networks pass through the revision barrier before a full snapshot is used.
func (session *providerSession) syncWatches(
	ctx context.Context,
	acceptCreatedBarrier bool,
) error {
	progressed := make(map[string]struct{}, len(session.networks))
	if acceptCreatedBarrier {
		for network, mirror := range session.networks {
			switch {
			case mirror.createdRev < mirror.rangeRev:
				return invalidRecord("Watch Created Revision 早于 Range Revision")
			case mirror.createdRev == mirror.rangeRev:
				progressed[network] = struct{}{}
			}
		}
	}
	if len(progressed) == len(session.networks) {
		return nil
	}
	progressCtx := clientv3.WithRequireLeader(session.watchCtx)
	requestCtx, cancel := context.WithTimeout(
		progressCtx,
		session.config.RequestTimeout,
	)
	err := session.client.RequestProgress(requestCtx)
	cancel()
	if err != nil {
		return operationError("watch progress barrier", err)
	}
	for len(progressed) != len(session.networks) {
		select {
		case <-ctx.Done():
			return wrapContext(ctx.Err())
		case envelope := <-session.events:
			if envelope.closed {
				return errs.ErrDiscoveryUnavailable
			}
			isProgress := envelope.response.IsProgressNotify()
			if _, ingestErr := session.ingest(envelope); ingestErr != nil {
				return ingestErr
			}
			if isProgress {
				progressed[envelope.network] = struct{}{}
			}
		}
	}
	return nil
}

func (session *providerSession) startWatch(network string) {
	mirror := session.networks[network]
	prefix := session.networkPrefix(network)
	watchCtx := clientv3.WithRequireLeader(session.watchCtx)
	channel := session.client.Watch(
		watchCtx,
		prefix,
		clientv3.WithPrefix(),
		clientv3.WithRev(mirror.rangeRev+1),
		clientv3.WithCreatedNotify(),
		clientv3.WithFragment(),
	)
	session.watchWG.Add(1)
	go func() {
		defer session.watchWG.Done()
		for {
			select {
			case <-session.watchCtx.Done():
				return
			case response, open := <-channel:
				if !open {
					session.sendEnvelope(watchEnvelope{
						network: network,
						closed:  true,
					})
					return
				}
				if !session.sendEnvelope(watchEnvelope{
					network:  network,
					response: response,
				}) {
					return
				}
			}
		}
	}()
}

func (session *providerSession) sendEnvelope(envelope watchEnvelope) bool {
	select {
	case session.events <- envelope:
		return true
	case <-session.watchCtx.Done():
		return false
	}
}

// ingest buffers event revisions until every watched prefix has advanced through them.
//
// The official client reassembles WithFragment responses before exposing WatchResponse, so a
// response's events are already complete. RequestProgress advances unaffected prefixes and makes
// a multi-prefix transaction visible to Host in one merged snapshot.
func (session *providerSession) ingest(
	envelope watchEnvelope,
) ([]publicprovider.Snapshot, error) {
	if envelope.closed {
		return nil, errs.ErrDiscoveryUnavailable
	}
	response := envelope.response
	if err := response.Err(); err != nil || response.Canceled {
		if err == nil {
			err = errs.ErrDiscoveryUnavailable
		}
		return nil, operationError("watch", err)
	}
	if response.Header == nil {
		return nil, invalidRecord("Watch Response Header 为空")
	}
	if err := session.checkHeader(response.Header); err != nil {
		return nil, err
	}
	mirror := session.networks[envelope.network]
	if mirror == nil {
		return nil, invalidRecord("Watch Network 未登记")
	}
	mirror.lastActivity = time.Now()
	if response.Created {
		mirror.createdRev = response.Header.Revision
		// Created Header 大于 Range Revision 时可能仍有历史事件尚未交付，不能直接
		// 用它越过事件屏障；初始同步只接受恰好等于 Range Revision 的 Created。
		return nil, nil
	}
	if response.Header.Revision < mirror.observedRev {
		return nil, invalidRecord("Watch Revision 回退")
	}
	mirror.observedRev = response.Header.Revision
	for _, event := range response.Events {
		if event == nil || event.Kv == nil || event.Kv.ModRevision <= 0 {
			return nil, invalidRecord("Watch Event 非法")
		}
		revision := event.Kv.ModRevision
		session.pending[revision] = append(
			session.pending[revision],
			watchMutation{network: envelope.network, event: event},
		)
	}
	if len(response.Events) > 0 {
		progressCtx := clientv3.WithRequireLeader(session.watchCtx)
		requestCtx, cancel := context.WithTimeout(
			progressCtx,
			session.config.RequestTimeout,
		)
		err := session.client.RequestProgress(requestCtx)
		cancel()
		if err != nil {
			return nil, operationError("watch progress", err)
		}
	}
	return session.flushPending()
}

func (session *providerSession) flushPending() ([]publicprovider.Snapshot, error) {
	if len(session.pending) == 0 {
		return nil, nil
	}
	minObserved := int64(^uint64(0) >> 1)
	for _, mirror := range session.networks {
		if mirror.observedRev < minObserved {
			minObserved = mirror.observedRev
		}
	}
	revisions := make([]int64, 0, len(session.pending))
	for revision := range session.pending {
		if revision <= minObserved {
			revisions = append(revisions, revision)
		}
	}
	slices.Sort(revisions)
	snapshots := make([]publicprovider.Snapshot, 0, len(revisions))
	for _, revision := range revisions {
		candidate := make(map[string]*networkMirror, len(session.networks))
		for network, mirror := range session.networks {
			candidate[network] = mirror
		}
		for _, mutation := range session.pending[revision] {
			mirror := candidate[mutation.network]
			if mirror == session.networks[mutation.network] {
				copyMirror := &networkMirror{
					records:      cloneRecords(mirror.records),
					rangeRev:     mirror.rangeRev,
					createdRev:   mirror.createdRev,
					observedRev:  mirror.observedRev,
					lastActivity: mirror.lastActivity,
				}
				candidate[mutation.network] = copyMirror
				mirror = copyMirror
			}
			if err := session.applyMutation(
				mutation.network,
				mirror.records,
				mutation.event,
			); err != nil {
				return nil, err
			}
		}
		snapshot, err := snapshotFrom(candidate)
		if err != nil {
			return nil, err
		}
		session.networks = candidate
		delete(session.pending, revision)
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (session *providerSession) applyMutation(
	network string,
	records map[string]publicprovider.Node,
	event *clientv3.Event,
) error {
	nodeID, err := session.parseNodeKey(network, event.Kv.Key)
	if err != nil {
		return err
	}
	switch event.Type {
	case mvccpb.PUT:
		recordNetwork, node, err := decodeRecord(event.Kv.Value)
		if err != nil {
			return err
		}
		if recordNetwork != network || node.NodeID != nodeID {
			return invalidRecord("Watch Key 与 Value 身份不一致")
		}
		records[nodeID] = node
	case mvccpb.DELETE:
		delete(records, nodeID)
	default:
		return invalidRecord("Watch Event 类型非法")
	}
	return nil
}

func (session *providerSession) snapshot() (publicprovider.Snapshot, error) {
	return snapshotFrom(session.networks)
}

func snapshotFrom(
	networks map[string]*networkMirror,
) (publicprovider.Snapshot, error) {
	nodes := make([]publicprovider.Node, 0)
	for _, mirror := range networks {
		for _, node := range mirror.records {
			nodes = append(nodes, node)
		}
	}
	return publicprovider.NormalizeSnapshot(publicprovider.Snapshot{Nodes: nodes})
}

func cloneRecords(
	source map[string]publicprovider.Node,
) map[string]publicprovider.Node {
	result := make(map[string]publicprovider.Node, len(source))
	for nodeID, node := range source {
		result[nodeID] = node
	}
	return result
}

func (session *providerSession) checkHeader(
	header *etcdserverpb.ResponseHeader,
) error {
	if header == nil || header.ClusterId == 0 {
		return errs.ErrDiscoveryUnavailable
	}
	if session.clusterID == 0 {
		session.clusterID = header.ClusterId
		return nil
	}
	if session.clusterID != header.ClusterId {
		return errs.Wrap(errs.CodeDiscoverySnapshotInvalid, errClusterMismatch)
	}
	return nil
}

func (session *providerSession) progressExpired() bool {
	deadline := time.Now().Add(-progressTimeout)
	for _, mirror := range session.networks {
		if mirror.lastActivity.Before(deadline) {
			return true
		}
	}
	return false
}

func (session *providerSession) networkPrefix(network string) string {
	return session.config.Namespace + "/v1/networks/" + network + "/nodes/"
}

func (session *providerSession) nodeKey(network, nodeID string) string {
	return session.networkPrefix(network) + nodeID
}

func (session *providerSession) parseNodeKey(
	network string,
	key []byte,
) (string, error) {
	prefix := session.networkPrefix(network)
	value := string(key)
	if !strings.HasPrefix(value, prefix) {
		return "", invalidRecord("Key Prefix 非法")
	}
	nodeID := strings.TrimPrefix(value, prefix)
	if !validToken(nodeID) || strings.Contains(nodeID, "/") {
		return "", invalidRecord("Key NodeID 非法")
	}
	return nodeID, nil
}

func (session *providerSession) close() {
	if session == nil {
		return
	}
	if session.progress != nil {
		stopTimer(session.progress)
	}
	if session.leaseCancel != nil {
		session.leaseCancel()
		session.leaseCancel = nil
	}
	if session.watchCancel != nil {
		session.watchCancel()
	}
	session.watchWG.Wait()
	if session.client != nil {
		_ = session.client.Close()
	}
}
