package kafkamodule

import (
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
)

type producerEnvelope struct {
	encoded  *encodedMessage
	delivery *Delivery
	message  any
	admitted chan struct{}
	finish   sync.Once
}

type submitQueue struct {
	mutex        sync.Mutex
	items        chan *producerEnvelope
	closed       bool
	messageLimit int
	byteLimit    int64
	messages     int
	bytes        int64
	active       map[*producerEnvelope]struct{}
}

func newSubmitQueue(messageLimit int, byteLimit int64) *submitQueue {
	return &submitQueue{items: make(chan *producerEnvelope, messageLimit), messageLimit: messageLimit, byteLimit: byteLimit, active: make(map[*producerEnvelope]struct{}, messageLimit)}
}

func (queue *submitQueue) trySubmit(envelope *producerEnvelope) error {
	if queue == nil || envelope == nil || envelope.encoded == nil || envelope.delivery == nil {
		return ErrInvalidArgument
	}
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if queue.closed {
		return ErrNotRunning
	}
	if envelope.encoded.payloadBytes > queue.byteLimit || queue.messages >= queue.messageLimit || queue.bytes > queue.byteLimit-envelope.encoded.payloadBytes {
		return errs.ErrTransportOverloaded
	}
	queue.messages++
	queue.bytes += envelope.encoded.payloadBytes
	queue.active[envelope] = struct{}{}
	queue.items <- envelope
	return nil
}

func (queue *submitQueue) release(envelope *producerEnvelope) {
	if queue == nil || envelope == nil || envelope.encoded == nil {
		return
	}
	queue.mutex.Lock()
	if _, exists := queue.active[envelope]; exists {
		delete(queue.active, envelope)
		queue.messages--
		queue.bytes -= envelope.encoded.payloadBytes
	}
	queue.mutex.Unlock()
}

func (queue *submitQueue) close() {
	if queue == nil {
		return
	}
	queue.mutex.Lock()
	if !queue.closed {
		queue.closed = true
		close(queue.items)
	}
	queue.mutex.Unlock()
}

func (queue *submitQueue) failAll(err error, complete func(*producerEnvelope, DeliveryResult)) {
	queue.mutex.Lock()
	active := make([]*producerEnvelope, 0, len(queue.active))
	for envelope := range queue.active {
		active = append(active, envelope)
	}
	queue.mutex.Unlock()
	for _, envelope := range active {
		complete(envelope, DeliveryResult{Err: err})
	}
}
