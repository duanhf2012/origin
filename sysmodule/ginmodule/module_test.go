package ginmodule

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/duanhf2012/origin/v3/service"
	"github.com/gin-gonic/gin"
)

func TestModuleLifecycleAndRegistrationGuards(t *testing.T) {
	var nilModule *Module
	if nilModule.OnInit() == nil || nilModule.OnStart(context.Background()) == nil ||
		nilModule.OnStop(context.Background()) == nil || nilModule.Addr() != nil ||
		nilModule.Stats() != (ServerStats{}) {
		t.Fatal("nil Module guards returned unexpected values")
	}

	module := &Module{}
	if module.OnInit() == nil || module.OnStart(context.Background()) == nil ||
		module.OnStart(nil) == nil || module.OnStop(nil) == nil {
		t.Fatal("unconfigured Module lifecycle guard succeeded")
	}
	if err := module.OnStop(context.Background()); err != nil {
		t.Fatalf("stopping an unstarted Module: %v", err)
	}
	if err := module.Setup("127.0.0.1:0", DefaultServerOptions()); err == nil {
		t.Fatal("Setup outside a bound Module.OnInit succeeded")
	}
	if err := validateAddress(" "); err == nil {
		t.Fatal("blank address was accepted")
	}

	assertPanics(t, func() { module.requireEngine() })
}

func TestRuntimeOptionsAreOwnedAndSetupIsSingleUse(t *testing.T) {
	trustedProxies := []string{"127.0.0.1"}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, nil
		},
	}
	options := DefaultServerOptions()
	options.TrustedProxies = trustedProxies
	options.TLSConfig = tlsConfig

	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		trustedProxies[0] = "10.0.0.1"
		tlsConfig.MinVersion = tls.VersionTLS12
		if module.Module.options.TrustedProxies[0] != "127.0.0.1" {
			t.Fatal("Setup retained the caller's trusted proxy slice")
		}
		if module.Module.options.TLSConfig == tlsConfig ||
			module.Module.options.TLSConfig.MinVersion != tls.VersionTLS13 {
			t.Fatal("Setup retained the caller's TLSConfig")
		}
		if err := module.Setup("127.0.0.1:0", DefaultServerOptions()); err == nil {
			t.Fatal("second Setup succeeded")
		}
		module.server.ErrorLog.Print("synthetic test error")
	})
	fixture.stop(t)
}

func TestInvalidTrustedProxyFailsInitialization(t *testing.T) {
	options := DefaultServerOptions()
	options.TrustedProxies = []string{"300.300.300.300/99"}
	module := &integrationModule{options: options}
	current, _ := newIntegrationNode(t, module, service.DefaultSchedulerConfig())
	if err := current.Start(context.Background()); err == nil {
		t.Fatal("invalid trusted proxy was accepted")
	}
	_ = current.Rollback(context.Background())
}

func TestRouteHandlerGuards(t *testing.T) {
	assertPanics(t, func() { routeHandlers(nil, nil) })
	assertPanics(t, func() {
		routeHandlers(func(*gin.Context) {}, []gin.HandlerFunc{nil})
	})
	assertPanics(t, func() { validateSafeHandlers(nil, nil) })
	assertPanics(t, func() {
		validateSafeHandlers(func(*SafeContext) {}, []SafeMiddlewareFunc{nil})
	})
	assertPanics(t, func() { validateSafeMiddleware([]SafeMiddlewareFunc{nil}) })
	assertPanics(t, func() { (&RouterGroup{}).requireGroup() })
	assertPanics(t, func() { (&SafeRouterGroup{}).requireGroup() })
}

func assertPanics(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("call did not panic")
		}
	}()
	call()
}
