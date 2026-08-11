package mongodbmodule

// Config 描述一个 MongoDB 集群和默认数据库。
//
// URI 是连接与普通 Driver 参数的唯一基础来源；Database 单独声明，便于同一 URI 在不同
// Service 中选择不同默认数据库。TLSCAFile 仅用于加载宿主机上的私有 CA，不能与 URI 中的
// CA、客户端证书或 WithTLSConfig 同时使用。
type Config struct {
	// URI 是标准 mongodb:// 或 mongodb+srv:// URI，可包含认证、Replica Set、连接池和超时参数。
	URI string `json:"uri" yaml:"uri"`
	// Database 是 Client 成功启动后 Database() 和 Collection() 使用的默认数据库名。
	Database string `json:"database" yaml:"database"`
	// TLSCAFile 是可选的 PEM CA 文件路径；留空时由 URI 和系统证书池决定 TLS 行为。
	TLSCAFile string `json:"tls_ca_file,omitempty" yaml:"tls_ca_file,omitempty"`
}
