package wsnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"

	gorillaws "github.com/gorilla/websocket"

	"github.com/duanhf2012/origin/v3/errs"
)

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func transportUnavailable(cause error) error {
	return errs.Wrap(errs.CodeTransportUnavailable, cause)
}

func deadlineError(cause error) error {
	return errs.Wrap(errs.CodeDeadlineExceeded, cause)
}

func contextError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return errs.Wrap(errs.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	default:
		return errs.Wrap(errs.CodeInternal, err)
	}
}

func normalizeIOError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorillaws.ErrReadLimit) {
		return errs.Wrap(errs.CodeTransportMessageTooLarge, err)
	}
	var closeErr *gorillaws.CloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case gorillaws.CloseNormalClosure, gorillaws.CloseGoingAway:
			return errs.Wrap(errs.CodeTransportClosed, err)
		case gorillaws.CloseMessageTooBig:
			return errs.Wrap(errs.CodeTransportMessageTooLarge, err)
		case gorillaws.CloseProtocolError, gorillaws.CloseUnsupportedData,
			gorillaws.CloseInvalidFramePayloadData:
			return errs.Wrap(errs.CodeTransportProtocol, err)
		default:
			return transportUnavailable(err)
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return deadlineError(err)
	}
	return transportUnavailable(err)
}

func normalizeHandlerError(err error) error {
	if err == nil {
		return nil
	}
	var coder errs.Coder
	if errors.As(err, &coder) {
		return err
	}
	return errs.Wrap(errs.CodeInternal, err)
}

func panicError(scope string, value any) error {
	cause := fmt.Errorf("%s panic: %v\n%s", scope, value, debug.Stack())
	return errs.Wrap(errs.CodeInternal, cause)
}

type slowClientError struct{}

func (slowClientError) Error() string    { return "wsnet: 慢连接持续超过高水位" }
func (slowClientError) SlowClient() bool { return true }

var _ error = slowClientError{}
