package admin

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/duanhf2012/origin/v3/errs"
)

// Response 是不可变的 Admin HTTP 输出。
type Response struct {
	status int
	header http.Header
	body   []byte
}

// JSON 将 value 编码为 2xx JSON 响应。
func JSON(status int, value any) (Response, error) {
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return Response{}, errs.NewMessage(errs.CodeInvalidArgument, "Admin JSON Response 状态必须是 2xx")
	}
	body, err := json.Marshal(value)
	if err != nil {
		return Response{}, errs.New(errs.CodeInternal)
	}
	return Response{
		status: status,
		header: http.Header{"Content-Type": {"application/json"}},
		body:   body,
	}, nil
}

// Empty 创建没有响应体的 Response。
func Empty(status int) Response {
	return Response{status: status}
}

// Status 返回固定的 HTTP 状态码。
func (response Response) Status() int { return response.status }

// Header 返回独立的 HTTP Header 副本。
func (response Response) Header() http.Header { return response.header.Clone() }

// Body 返回独立的已编码响应体副本。
func (response Response) Body() []byte { return bytes.Clone(response.body) }

// encodedHeader 仅供 Admin Runtime 读取已冻结的 Header；调用方不得修改结果。
func (response Response) encodedHeader() http.Header { return response.header }

// encodedBody 仅供 Admin Runtime 读取已冻结的响应字节；调用方不得修改结果。
func (response Response) encodedBody() []byte { return response.body }
