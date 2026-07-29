package etcd

import (
	"bytes"
	"context"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (session *providerSession) publish(
	ctx context.Context,
	node publicprovider.Node,
) error {
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	encoded, err := encodeRecord(session.config.LocalNetwork, node)
	if err != nil {
		return err
	}
	if err := session.ensureLease(ctx); err != nil {
		return err
	}
	key := session.nodeKey(session.config.LocalNetwork, node.NodeID)
	for {
		requestCtx, cancel := context.WithTimeout(
			ctx,
			session.config.RequestTimeout,
		)
		response, err := session.client.Get(requestCtx, key, clientv3.WithLimit(1))
		cancel()
		if err != nil {
			return operationError("read publish owner", err)
		}
		if err := session.checkHeader(response.Header); err != nil {
			return err
		}
		switch len(response.Kvs) {
		case 0:
			requestCtx, cancel = context.WithTimeout(
				ctx,
				session.config.RequestTimeout,
			)
			transaction, txnErr := session.client.Txn(requestCtx).
				If(clientv3.Compare(clientv3.Version(key), "=", 0)).
				Then(clientv3.OpPut(
					key,
					string(encoded),
					clientv3.WithLease(session.leaseID),
				)).
				Commit()
			cancel()
			if txnErr != nil {
				return operationError("create published record", txnErr)
			}
			if err := session.checkHeader(transaction.Header); err != nil {
				return err
			}
			if transaction.Succeeded {
				return nil
			}
		case 1:
			current := response.Kvs[0]
			network, existing, decodeErr := decodeRecord(current.Value)
			if decodeErr != nil {
				return decodeErr
			}
			if network != session.config.LocalNetwork ||
				existing.NodeID != node.NodeID {
				return invalidRecord("发布 Key 与现有 Value 不一致")
			}
			if existing.SessionID != node.SessionID {
				session.releaseLease(context.Background())
				return errs.ErrDiscoveryDuplicateNode
			}
			if bytes.Equal(current.Value, encoded) &&
				current.Lease == int64(session.leaseID) {
				return nil
			}
			requestCtx, cancel = context.WithTimeout(
				ctx,
				session.config.RequestTimeout,
			)
			transaction, txnErr := session.client.Txn(requestCtx).
				If(clientv3.Compare(
					clientv3.ModRevision(key),
					"=",
					current.ModRevision,
				)).
				Then(clientv3.OpPut(
					key,
					string(encoded),
					clientv3.WithLease(session.leaseID),
				)).
				Commit()
			cancel()
			if txnErr != nil {
				return operationError("update published record", txnErr)
			}
			if err := session.checkHeader(transaction.Header); err != nil {
				return err
			}
			if transaction.Succeeded {
				return nil
			}
		default:
			return invalidRecord("精确 Key 返回多条记录")
		}
		if err := ctx.Err(); err != nil {
			return wrapContext(err)
		}
	}
}

func (session *providerSession) withdraw(ctx context.Context) error {
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	key := session.nodeKey(
		session.config.LocalNetwork,
		session.nodeID,
	)
	for {
		requestCtx, cancel := context.WithTimeout(
			ctx,
			session.config.RequestTimeout,
		)
		response, err := session.client.Get(requestCtx, key, clientv3.WithLimit(1))
		cancel()
		if err != nil {
			return operationError("read withdraw owner", err)
		}
		if err := session.checkHeader(response.Header); err != nil {
			return err
		}
		if len(response.Kvs) == 0 {
			session.releaseLease(ctx)
			return nil
		}
		current := response.Kvs[0]
		network, node, err := decodeRecord(current.Value)
		if err != nil {
			return err
		}
		if network != session.config.LocalNetwork ||
			node.NodeID != session.nodeID {
			return invalidRecord("撤销 Key 与现有 Value 不一致")
		}
		if node.SessionID != session.sessionID {
			session.releaseLease(ctx)
			return nil
		}
		requestCtx, cancel = context.WithTimeout(
			ctx,
			session.config.RequestTimeout,
		)
		transaction, txnErr := session.client.Txn(requestCtx).
			If(clientv3.Compare(
				clientv3.ModRevision(key),
				"=",
				current.ModRevision,
			)).
			Then(clientv3.OpDelete(key)).
			Commit()
		cancel()
		if txnErr != nil {
			return operationError("delete published record", txnErr)
		}
		if err := session.checkHeader(transaction.Header); err != nil {
			return err
		}
		if transaction.Succeeded {
			session.releaseLease(ctx)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return wrapContext(err)
		}
	}
}

func (session *providerSession) ensureLease(ctx context.Context) error {
	if session.leaseID != clientv3.NoLease && session.keepAlive != nil {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(
		ctx,
		session.config.RequestTimeout,
	)
	response, err := session.client.Grant(
		requestCtx,
		int64(session.config.TTL/time.Second),
	)
	cancel()
	if err != nil {
		return operationError("grant lease", err)
	}
	if response == nil || response.ID == clientv3.NoLease || response.TTL <= 0 {
		return errs.ErrDiscoveryUnavailable
	}
	if err := session.checkHeader(response.ResponseHeader); err != nil {
		return err
	}
	leaseCtx, leaseCancel := context.WithCancel(session.watchCtx)
	channel, err := session.client.KeepAlive(leaseCtx, response.ID)
	if err != nil {
		leaseCancel()
		return operationError("start lease keepalive", err)
	}
	firstCtx, firstCancel := context.WithTimeout(
		ctx,
		session.config.RequestTimeout,
	)
	defer firstCancel()
	select {
	case first, open := <-channel:
		if !open || first == nil || first.TTL <= 0 ||
			first.ID != response.ID {
			leaseCancel()
			return errs.ErrDiscoveryUnavailable
		}
		if err := session.checkHeader(first.ResponseHeader); err != nil {
			leaseCancel()
			return err
		}
	case <-firstCtx.Done():
		leaseCancel()
		return wrapContext(firstCtx.Err())
	case <-session.watchCtx.Done():
		leaseCancel()
		return errs.ErrServiceStopped
	}
	session.leaseID = response.ID
	session.leaseCancel = leaseCancel
	session.keepAlive = channel
	return nil
}

func (session *providerSession) releaseLease(ctx context.Context) {
	leaseID := session.leaseID
	if session.leaseCancel != nil {
		session.leaseCancel()
	}
	session.leaseID = clientv3.NoLease
	session.leaseCancel = nil
	session.keepAlive = nil
	if leaseID == clientv3.NoLease || session.client == nil {
		return
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(
		ctx,
		session.config.RequestTimeout,
	)
	response, err := session.client.Revoke(requestCtx, leaseID)
	cancel()
	if err == nil && response != nil {
		_ = session.checkHeader(response.Header)
	}
}
