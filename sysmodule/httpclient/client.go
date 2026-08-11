package httpclient

import (
	"errors"
	"io"
	"net/http"

	"github.com/duanhf2012/origin/v3/errs"
)

// ErrResponseBodyTooLarge 表示 DoBytes 解压后的响应 Body 超过配置上限。
var ErrResponseBodyTooLarge = errors.New("httpclient: response body too large")

// Response 是 DoBytes 完整读取并关闭标准响应 Body 后返回的调用方私有快照。
type Response struct {
	// StatusCode 是标准 HTTP 响应状态码；4xx/5xx 不会自动转换为 Go error。
	StatusCode int
	// Header 是独立于标准 http.Response 的调用方私有克隆。
	Header http.Header
	// Body 是已经完整读取且受 MaxResponseBodySize 限制的调用方私有数据。
	Body []byte
}

// Client 包装一个可并发复用的标准 HTTP Client。
type Client struct {
	client              *http.Client
	maxResponseBodySize int64
}

// New 校验 Options 并创建 Client。Transport 为空时为当前 Client 创建私有连接池。
func New(options Options) (*Client, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	transport := options.Transport
	if transport == nil {
		var err error
		transport, err = NewTransport(DefaultTransportOptions())
		if err != nil {
			return nil, err
		}
	}
	return &Client{
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: options.CheckRedirect,
			Jar:           options.Jar,
			Timeout:       options.Timeout,
		},
		maxResponseBodySize: options.MaxResponseBodySize,
	}, nil
}

// Do 在调用方 goroutine 中执行标准 http.Client.Do，并保留响应 Body 的流式所有权语义。
//
// 成功返回后调用方必须读取并关闭 response.Body。Client 可以被多个 goroutine 并发调用。
func (client *Client) Do(request *http.Request) (*http.Response, error) {
	if client == nil || client.client == nil || request == nil {
		return nil, errs.ErrInvalidArgument
	}
	return client.client.Do(request)
}

// DoBytes 在调用方 goroutine 中完整读取有界响应、关闭 Body，并返回独立 Header/Body 快照。
//
// 4xx/5xx 仍作为普通 Response 返回；读取、超限和关闭错误通过 error 返回。
func (client *Client) DoBytes(request *http.Request) (Response, error) {
	response, err := client.Do(request)
	if err != nil {
		return Response{}, err
	}
	body, readErr := readBounded(response.Body, client.maxResponseBodySize)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return Response{}, errors.Join(readErr, closeErr)
	}
	return Response{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       body,
	}, nil
}

// CloseIdleConnections 在调用方 goroutine 中请求底层 Transport 关闭空闲连接，不中断活动请求。
//
// 注入共享 Transport 时，调用方必须统一决定调用时机；关闭后 Client 仍可继续使用。
func (client *Client) CloseIdleConnections() {
	if client == nil || client.client == nil {
		return
	}
	client.client.CloseIdleConnections()
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if limited.N > 0 {
		return body, nil
	}
	var extra [1]byte
	count, err := io.ReadFull(reader, extra[:])
	if count > 0 {
		return nil, ErrResponseBodyTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return body, nil
}
