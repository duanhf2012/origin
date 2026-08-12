package kafkamodule

// ProducerStats 是受管 Producer 的无锁原子快照。
type ProducerStats struct {
	// Accepted 是已进入受管发送流程的累计消息数。
	Accepted uint64
	// Succeeded 是 Broker 已确认成功的累计消息数。
	Succeeded uint64
	// Failed 是已得到最终失败结果的累计消息数。
	Failed uint64
	// Overloaded 是因消息数或字节预算不足而被拒绝的累计消息数。
	Overloaded uint64
	// InFlight 是已接受但尚未得到最终 Delivery 的当前消息数。
	InFlight int64
}

// ConsumerStats 是受管 Consumer 的无锁原子快照。
type ConsumerStats struct {
	// Received 是从 Sarama Claim 收到的累计消息数。
	Received uint64
	// Handled 是业务 Handler 成功完成的累计消息数。
	Handled uint64
	// Failed 是业务 Handler 最终失败的累计消息数。
	Failed uint64
	// Batches 是批量 Handler 成功完成的累计批次数。
	Batches uint64
	// Rebalances 是成功建立 Consumer Group Session 的累计次数。
	Rebalances uint64
	// DispatchRejected 是无法投递到所属 Service 工作协程的累计次数。
	DispatchRejected uint64
	// Running 表示当前是否存在运行中的 Consumer Runtime。
	Running bool
}
