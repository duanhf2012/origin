package kafkamodule

// ProducerStats 是受管 Producer 的无锁原子快照。
type ProducerStats struct {
	Accepted   uint64
	Succeeded  uint64
	Failed     uint64
	Overloaded uint64
	InFlight   int64
}

// ConsumerStats 是受管 Consumer 的无锁原子快照。
type ConsumerStats struct {
	Received         uint64
	Handled          uint64
	Failed           uint64
	Batches          uint64
	Rebalances       uint64
	DispatchRejected uint64
	Running          bool
}
