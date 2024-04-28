package kafkamodule

import "github.com/IBM/sarama"

type KafkaAdmin struct {
	sarama.ClusterAdmin

	mapTopic map[string]sarama.TopicDetail
}

func (ka *KafkaAdmin) Setup(kafkaVersion string, addrs []string) error {
	config := sarama.NewConfig()
	var err error
	config.Version, err = sarama.ParseKafkaVersion(kafkaVersion)
	if err != nil {
		return err
	}

	ka.ClusterAdmin, err = sarama.NewClusterAdmin(addrs, config)
	if err != nil {
		return err
	}

	ka.mapTopic, err = ka.GetTopics()
	if err != nil {
		ka.ClusterAdmin.Close()
		return err
	}

	return nil
}

func (ka *KafkaAdmin) RefreshTopic() error {
	var err error
	ka.mapTopic, err = ka.GetTopics()

	return err
}

func (ka *KafkaAdmin) HasTopic(topic string) bool {
	_, ok := ka.mapTopic[topic]

	return ok
}

func (ka *KafkaAdmin) GetTopicDetail(topic string) *sarama.TopicDetail {
	topicDetail, ok := ka.mapTopic[topic]
	if ok == false {
		return nil
	}

	return &topicDetail
}

func (ka *KafkaAdmin) GetTopics() (map[string]sarama.TopicDetail, error) {
	return ka.ListTopics()
}

// CreateTopic 创建主题
// numPartitions分区数
// replicationFactor副本数
// validateOnly参数执行操作时只进行参数验证而不实际执行操作
func (ka *KafkaAdmin) CreateTopic(topic string, numPartitions int32, replicationFactor int16, validateOnly bool) error {
	return ka.ClusterAdmin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: numPartitions, ReplicationFactor: replicationFactor}, validateOnly)
}
