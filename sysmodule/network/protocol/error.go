package protocol

import "github.com/duanhf2012/origin/v3/errs"

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func protocolError(message string) error {
	return errs.NewMessage(errs.CodeTransportProtocol, message)
}
