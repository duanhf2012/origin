package rpc

import (
	"context"
	"strconv"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
)

type natsSystemPeer struct {
	system  *systemRuntime
	target  SystemTarget
	reply   string
	handler SystemHandler
	server  bool

	mu          sync.Mutex
	closed      bool
	opened      bool
	counterpart *natsSystemPeer
}

func (peer *natsSystemPeer) Send(payload []byte) error {
	if peer == nil || len(payload) == 0 || len(payload) > MaxSystemMessageSize {
		return errs.ErrInvalidArgument
	}
	peer.mu.Lock()
	closed := peer.closed
	loopback := peer.counterpart
	peer.mu.Unlock()
	if closed {
		return errs.ErrServiceStopped
	}
	if loopback != nil {
		loopback.deliver(payload)
		return nil
	}
	if peer.system == nil || peer.system.owner == nil || peer.system.owner.nats == nil {
		return errs.ErrTransportUnavailable
	}
	conn, err := peer.system.owner.nats.connectedConn()
	if err != nil {
		return err
	}
	namespace := peer.system.owner.nats.config.NATS.Namespace
	if peer.server {
		if peer.reply == "" {
			return errs.ErrTransportUnavailable
		}
		return conn.Publish(peer.reply, payload)
	}
	return conn.PublishRequest(
		natsSystemServerSubject(namespace, peer.target.NodeID),
		peer.reply,
		payload,
	)
}

func (peer *natsSystemPeer) Close() {
	if peer == nil {
		return
	}
	peer.mu.Lock()
	closed := peer.closed
	server := peer.server
	counterpart := peer.counterpart
	system := peer.system
	target := peer.target
	peer.mu.Unlock()
	if !closed && !server && counterpart == nil {
		peer.notifyRemoteClose(system, target)
	}
	peer.closeWith(errs.ErrTransportClosed)
	if counterpart != nil {
		counterpart.closeWith(errs.ErrTransportClosed)
	}
}

// notifyRemoteClose uses a reserved empty NATS request to release the server
// peer. SystemPeer.Send rejects empty payloads, so user control messages can
// never be confused with this transport-level close signal.
func (peer *natsSystemPeer) notifyRemoteClose(
	system *systemRuntime,
	target SystemTarget,
) {
	if peer == nil || system == nil || system.owner == nil ||
		system.owner.nats == nil || target.NodeID == "" {
		return
	}
	conn, err := system.owner.nats.connectedConn()
	if err != nil {
		return
	}
	namespace := system.owner.nats.config.NATS.Namespace
	_ = conn.PublishRequest(
		natsSystemServerSubject(namespace, target.NodeID),
		peer.reply,
		nil,
	)
}

func (peer *natsSystemPeer) deliver(payload []byte) {
	if peer == nil || len(payload) > MaxSystemMessageSize {
		return
	}
	peer.mu.Lock()
	closed := peer.closed
	handler := peer.handler
	peer.mu.Unlock()
	if !closed && handler != nil {
		handler.OnSystemMessage(peer, payload)
	}
}

func (peer *natsSystemPeer) closeWith(cause error) {
	if peer == nil {
		return
	}
	peer.mu.Lock()
	if peer.closed {
		peer.mu.Unlock()
		return
	}
	peer.closed = true
	handler := peer.handler
	system := peer.system
	peer.mu.Unlock()
	if system != nil {
		system.removeNATSPeer(peer)
	}
	if handler != nil {
		handler.OnSystemClose(peer, cause)
	}
}

func (system *systemRuntime) dialNATS(
	target SystemTarget,
	handler SystemHandler,
) (SystemPeer, error) {
	if system == nil || system.owner == nil || system.owner.nats == nil {
		return nil, errs.ErrTransportUnavailable
	}
	if _, err := system.owner.nats.connectedConn(); err != nil {
		return nil, err
	}
	system.mu.Lock()
	if system.closed || (system.natsPeer != nil && !system.natsPeer.isClosed()) {
		system.mu.Unlock()
		return nil, errs.ErrServiceNotReady
	}
	system.natsDialID++
	if system.natsDialID == 0 {
		system.natsDialID++
	}
	peer := &natsSystemPeer{
		system: system,
		target: target,
		reply: natsSystemClientSubject(
			system.owner.nats.config.NATS.Namespace,
			system.owner.nodeID,
			system.owner.sessionID,
			system.natsDialID,
		),
		handler: handler,
	}
	system.natsPeer = peer
	inbound := system.handler
	if target.NodeID == system.owner.nodeID && inbound != nil {
		server := &natsSystemPeer{
			system:  system,
			target:  target,
			handler: inbound,
			server:  true,
		}
		peer.counterpart = server
		server.counterpart = peer
		system.natsInbound["local"] = server
	}
	system.mu.Unlock()
	if peer.counterpart != nil {
		peer.counterpart.handler.OnSystemOpen(peer.counterpart)
	}
	handler.OnSystemOpen(peer)
	return peer, nil
}

func (system *systemRuntime) setupNATS(
	ctx context.Context,
	conn *natsnet.Conn,
	namespace string,
	queueMessages int,
) ([]*natsnet.Subscription, error) {
	if system == nil || conn == nil {
		return nil, errs.ErrInvalidArgument
	}
	options := natsnet.SubscriptionOptions{PendingMessages: queueMessages}
	client, err := conn.Subscribe(
		ctx,
		natsSystemClientSubscriptionSubject(
			namespace,
			system.owner.nodeID,
			system.owner.sessionID,
		),
		options,
		func(message natsnet.Message) { system.handleNATSResponse(message) },
	)
	if err != nil {
		return nil, err
	}
	result := []*natsnet.Subscription{client}
	if system.inboundHandler() == nil {
		return result, nil
	}
	server, err := conn.Subscribe(
		ctx,
		natsSystemServerSubject(namespace, system.owner.nodeID),
		options,
		func(message natsnet.Message) { system.handleNATSInbound(message) },
	)
	if err != nil {
		client.Close()
		return nil, err
	}
	return append(result, server), nil
}

func (system *systemRuntime) handleNATSResponse(message natsnet.Message) {
	if system == nil || len(message.Data) > MaxSystemMessageSize {
		return
	}
	system.mu.Lock()
	peer := system.natsPeer
	system.mu.Unlock()
	if peer != nil && message.Subject == peer.reply {
		peer.deliver(message.Data)
	}
}

func (system *systemRuntime) handleNATSInbound(message natsnet.Message) {
	if system == nil || message.Reply == "" || len(message.Data) > MaxSystemMessageSize {
		return
	}
	handler := system.inboundHandler()
	if handler == nil {
		return
	}
	system.mu.Lock()
	if system.closed {
		system.mu.Unlock()
		return
	}
	peer := system.natsInbound[message.Reply]
	if peer == nil {
		peer = &natsSystemPeer{
			system:  system,
			reply:   message.Reply,
			handler: handler,
			server:  true,
		}
		system.natsInbound[message.Reply] = peer
	}
	system.mu.Unlock()
	if len(message.Data) == 0 {
		peer.closeWith(errs.ErrTransportClosed)
		return
	}
	peer.mu.Lock()
	first := !peer.closed && !peer.opened
	peer.opened = true
	peer.mu.Unlock()
	if first {
		handler.OnSystemOpen(peer)
	}
	peer.deliver(message.Data)
}

func (system *systemRuntime) notifyNATSDisconnected(cause error) {
	if system == nil {
		return
	}
	system.mu.Lock()
	peers := make([]*natsSystemPeer, 0, len(system.natsInbound)+1)
	if system.natsPeer != nil {
		peers = append(peers, system.natsPeer)
		system.natsPeer = nil
	}
	for _, peer := range system.natsInbound {
		peers = append(peers, peer)
	}
	system.natsInbound = make(map[string]*natsSystemPeer)
	system.mu.Unlock()
	for _, peer := range peers {
		peer.closeWith(cause)
	}
}

func (peer *natsSystemPeer) isClosed() bool {
	if peer == nil {
		return true
	}
	peer.mu.Lock()
	closed := peer.closed
	peer.mu.Unlock()
	return closed
}

func (system *systemRuntime) removeNATSPeer(peer *natsSystemPeer) {
	if system == nil || peer == nil {
		return
	}
	system.mu.Lock()
	if system.natsPeer == peer {
		system.natsPeer = nil
	}
	for key, current := range system.natsInbound {
		if current == peer {
			delete(system.natsInbound, key)
		}
	}
	system.mu.Unlock()
}

func natsSystemServerSubject(namespace, nodeID string) string {
	return "orpc." + namespace + ".sys." + SystemServiceDiscovery + ".server." + nodeID
}

func natsSystemClientSubject(
	namespace, nodeID string,
	sessionID, dialID uint64,
) string {
	return natsSystemClientSubjectPrefix(namespace, nodeID, sessionID) +
		"." + strconv.FormatUint(dialID, 10)
}

func natsSystemClientSubscriptionSubject(
	namespace, nodeID string,
	sessionID uint64,
) string {
	return natsSystemClientSubjectPrefix(namespace, nodeID, sessionID) + ".*"
}

func natsSystemClientSubjectPrefix(
	namespace, nodeID string,
	sessionID uint64,
) string {
	return "orpc." + namespace + ".sys." + SystemServiceDiscovery +
		".client." + nodeID + "." + strconv.FormatUint(sessionID, 10)
}
