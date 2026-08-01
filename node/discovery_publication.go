package node

import (
	"context"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// discoveryPublication 是每个 Node 唯一的完整发现快照发布者。
//
// desired 只保存代次，不保存可变快照；发布循环每次读取 Service 原子状态重新构造完整
// 事实。容量一 wake 合并尚未开始的多次变化，进行中的发布后若出现更新则再发布最新代次。
type discoveryPublication struct {
	node *Node

	mu           sync.Mutex
	desired      uint64
	processed    uint64
	acknowledged uint64
	failed       uint64
	lastError    error
	changed      chan struct{}
	closed       bool

	wake   chan struct{}
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	start  sync.Once
}

func newDiscoveryPublication(node *Node) *discoveryPublication {
	ctx, cancel := context.WithCancel(context.Background())
	return &discoveryPublication{
		node:    node,
		changed: make(chan struct{}),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (publication *discoveryPublication) startPublisher() {
	if publication == nil {
		return
	}
	publication.start.Do(func() { go publication.run() })
}

func (publication *discoveryPublication) request(ctx context.Context) error {
	if publication == nil {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	target, err := publication.enqueue()
	if err != nil {
		return err
	}
	return publication.wait(ctx, target)
}

func (publication *discoveryPublication) wait(ctx context.Context, target uint64) error {
	if publication == nil || target == 0 {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	for {
		publication.mu.Lock()
		if publication.acknowledged >= target {
			publication.mu.Unlock()
			return nil
		}
		if publication.closed {
			publication.mu.Unlock()
			return errs.ErrServiceStopping
		}
		// 若失败后已有更新代次，继续等待更新代次的 ACK；它同样确认当前调用所需事实。
		if publication.failed >= target && publication.desired <= publication.failed {
			err := publication.lastError
			publication.mu.Unlock()
			return err
		}
		changed := publication.changed
		publication.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
		}
	}
}

func (publication *discoveryPublication) run() {
	defer close(publication.done)
	defer func() {
		publication.mu.Lock()
		if !publication.closed {
			publication.closed = true
			close(publication.changed)
		}
		publication.mu.Unlock()
	}()
	for {
		select {
		case <-publication.wake:
		case <-publication.ctx.Done():
			return
		}
		for {
			publication.mu.Lock()
			target := publication.desired
			if target <= publication.processed {
				publication.mu.Unlock()
				break
			}
			publication.mu.Unlock()

			publishCtx, cancel := context.WithTimeout(publication.ctx, 5*time.Second)
			err := publication.node.publishDiscoveryContext(publishCtx)
			cancel()

			publication.mu.Lock()
			publication.processed = target
			if err == nil {
				publication.acknowledged = target
				publication.lastError = nil
			} else {
				publication.failed = target
				publication.lastError = err
			}
			close(publication.changed)
			publication.changed = make(chan struct{})
			more := publication.desired > target
			publication.mu.Unlock()
			publication.node.updateDiscoveryAvailable(err == nil)
			if err != nil {
				publication.node.logger.Error(
					"动态服务发现发布失败",
					originlog.Err(err),
				)
			}
			if !more {
				break
			}
		}
	}
}

func (publication *discoveryPublication) enqueue() (uint64, error) {
	if publication == nil {
		return 0, nil
	}
	publication.startPublisher()
	publication.mu.Lock()
	if publication.closed {
		publication.mu.Unlock()
		return 0, errs.ErrServiceStopping
	}
	publication.desired++
	target := publication.desired
	publication.mu.Unlock()
	publication.signal()
	return target, nil
}

func (node *Node) requestDiscoveryPublication(ctx context.Context) error {
	if node == nil || node.discoveryPublication == nil {
		return nil
	}
	return node.discoveryPublication.request(ctx)
}

func (publication *discoveryPublication) signal() {
	select {
	case publication.wake <- struct{}{}:
	default:
	}
}

func (node *Node) stopDiscoveryPublication() {
	if node == nil || node.discoveryPublication == nil {
		return
	}
	publication := node.discoveryPublication
	publication.startPublisher()
	publication.cancel()
	<-publication.done
}
