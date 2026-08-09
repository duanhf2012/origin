package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/duanhf2012/origin/v3/errs"
)

// Request 是 Handler 可安全持有的 Admin HTTP 输入。
type Request struct {
	id        string
	principal Principal
	query     url.Values
	header    http.Header
	body      []byte
}

// NewRequest 复制所有输入，使后续 HTTP Runtime 缓冲复用不会影响 Handler。
func NewRequest(
	id string,
	principal Principal,
	query url.Values,
	header http.Header,
	body []byte,
) Request {
	return Request{
		id:        id,
		principal: clonePrincipal(principal),
		query:     cloneQuery(query),
		header:    header.Clone(),
		body:      bytes.Clone(body),
	}
}

// ID 返回请求的关联 ID。
func (request Request) ID() string { return request.id }

// Principal 返回独立的认证主体副本。
func (request Request) Principal() Principal { return clonePrincipal(request.principal) }

// Query 返回独立的 URL Query 副本。
func (request Request) Query() url.Values { return cloneQuery(request.query) }

// Header 返回独立的 HTTP Header 副本。
func (request Request) Header() http.Header { return request.header.Clone() }

// Body 返回独立的原始请求体副本。
func (request Request) Body() []byte { return bytes.Clone(request.body) }

// DecodeJSON 只接受一个字段已知的 JSON 值，并为所有输入错误标注稳定错误码。
func (request Request) DecodeJSON(target any) error {
	decoder := json.NewDecoder(bytes.NewReader(request.body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errs.New(errs.CodeInvalidArgument)
		}
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}
	return nil
}

func clonePrincipal(principal Principal) Principal {
	return Principal{
		Subject:    principal.Subject,
		Roles:      append([]string(nil), principal.Roles...),
		Attributes: cloneAttributes(principal.Attributes),
	}
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}

func cloneQuery(query url.Values) url.Values {
	if query == nil {
		return nil
	}
	cloned := make(url.Values, len(query))
	for key, values := range query {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
