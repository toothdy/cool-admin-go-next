package grpc

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/gogf/gf/contrib/rpc/grpcx/v2"
	"github.com/gogf/gf/v2/net/gsvc"
	grpcstd "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 生成的 protobuf 服务注册桥接
type GRPCRegistrar interface {
	Register(*grpcx.GrpcServer) error
}

// GoFrame gRPC 承载适配器
type Transport struct {
	config          Config
	listener        net.Listener
	mu              sync.Mutex
	newRegistry     func() gsvc.Registrar
	preparedService gsvc.Service
	registrar       GRPCRegistrar
	registry        gsvc.Registrar
	server          *grpcx.GrpcServer
	state           transportState
	stopDone        chan struct{}
	stopErr         error
	stopOnce        sync.Once
	stopping        atomic.Bool
	stream          []grpcstd.StreamServerInterceptor
	terminated      chan error
	terminationOnce sync.Once
	unary           []grpcstd.UnaryServerInterceptor
}

type transportState uint8

const (
	stateNew transportState = iota
	statePrepared
	stateStarted
	stateStopping
	stateStopped
)

// 构造 Transport
func New(
	config Config,
	registrar GRPCRegistrar,
	unary []grpcstd.UnaryServerInterceptor,
	stream []grpcstd.StreamServerInterceptor,
) (*Transport, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if registrar == nil {
		return nil, exception.Core("gRPC Registrar 不能为空")
	}

	return newTransport(config, registrar, unary, stream, func() gsvc.Registrar {
		return gsvc.GetRegistry()
	}), nil
}

// 固定 Transport 名称
func (transport *Transport) Name() string { return "grpc" }

// 是否启用
func (transport *Transport) Enabled() bool {
	return transport != nil && transport.config.Enabled
}

// 绑定端口并注册 protobuf 服务
func (transport *Transport) Prepare(ctx context.Context) error {
	if transport == nil {
		return exception.Core("gRPC Transport 未初始化")
	}
	if !transport.config.Enabled {
		return exception.Core("gRPC Transport 未启用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.state != stateNew {
		return exception.Core("gRPC Transport 已准备或停止")
	}
	listener, err := net.Listen("tcp", transport.listenAddress())
	if err != nil {
		return exception.WrapCore(err, "绑定 gRPC 监听地址失败")
	}
	server := transport.newServer(app.Readiness(ctx))
	if err = transport.registrar.Register(server); err != nil {
		_ = listener.Close()
		return exception.WrapCore(err, "注册 gRPC protobuf 服务失败")
	}
	transport.listener = listener
	transport.server = server
	transport.state = statePrepared

	return nil
}

// 服务循环并返回可观察终止 Channel
func (transport *Transport) Start(ctx context.Context) (<-chan error, error) {
	if transport == nil {
		return nil, exception.Core("gRPC Transport 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	transport.mu.Lock()
	if transport.state != statePrepared {
		transport.mu.Unlock()
		return nil, exception.Core("gRPC Transport 尚未准备或已启动")
	}
	listener := transport.listener
	server := transport.server
	if transport.config.Registry {
		transport.registry = transport.newRegistry()
		if transport.registry == nil {
			transport.mu.Unlock()
			return nil, transport.failStart(ctx, exception.Core("gRPC Registry 未配置"))
		}
		service := newService(transport.config.Name, listener.Addr().String())
		registered, err := transport.registry.Register(ctx, service)
		if err != nil {
			transport.mu.Unlock()
			return nil, transport.failStart(ctx, exception.WrapCore(err, "注册 gRPC 服务发现记录失败"))
		}
		if registered == nil {
			transport.mu.Unlock()
			return nil, transport.failStart(ctx, exception.Core("gRPC Registry 返回空服务记录"))
		}
		transport.preparedService = registered
	}
	transport.state = stateStarted
	transport.mu.Unlock()

	go transport.serve(server.Server, listener)

	return transport.terminated, nil
}

// 注销服务并停止接收请求
func (transport *Transport) Stop(ctx context.Context) error {
	if transport == nil {
		return nil
	}
	transport.stopOnce.Do(func() {
		transport.stopErr = transport.stop(ctx)
		transport.closeTermination()
		close(transport.stopDone)
	})
	<-transport.stopDone

	return transport.stopErr
}

func newTransport(
	config Config,
	registrar GRPCRegistrar,
	unary []grpcstd.UnaryServerInterceptor,
	stream []grpcstd.StreamServerInterceptor,
	registry func() gsvc.Registrar,
) *Transport {
	return &Transport{
		config:      config,
		newRegistry: registry,
		registrar:   registrar,
		stopDone:    make(chan struct{}),
		stream:      append([]grpcstd.StreamServerInterceptor(nil), stream...),
		terminated:  make(chan error, 1),
		unary:       append([]grpcstd.UnaryServerInterceptor(nil), unary...),
	}
}

func (transport *Transport) newServer(readiness app.ReadyState) *grpcx.GrpcServer {
	unary := append([]grpcstd.UnaryServerInterceptor{unaryReady(readiness), unaryError}, transport.unary...)
	stream := append([]grpcstd.StreamServerInterceptor{streamReady(readiness), streamError}, transport.stream...)
	config := &grpcx.GrpcServerConfig{
		Name:    transport.config.Name,
		Address: transport.listenAddress(),
		Options: []grpcstd.ServerOption{
			grpcx.Server.ChainUnary(unary...),
			grpcx.Server.ChainStream(stream...),
		},
	}

	return grpcx.Server.New(config)
}

func (transport *Transport) serve(server *grpcstd.Server, listener net.Listener) {
	err := server.Serve(listener)
	if transport.stopping.Load() || errors.Is(err, grpcstd.ErrServerStopped) {
		return
	}
	if err == nil {
		err = exception.Core("gRPC Serve 意外结束")
	} else {
		err = exception.WrapCore(err, "gRPC Serve 异常终止")
	}
	transport.terminate(err)
}

func (transport *Transport) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	transport.stopping.Store(true)

	transport.mu.Lock()
	state := transport.state
	listener := transport.listener
	server := transport.server
	registry := transport.registry
	service := transport.preparedService
	if state == stateNew || state == stateStopped {
		transport.state = stateStopped
		transport.mu.Unlock()
		return nil
	}
	transport.state = stateStopping
	transport.mu.Unlock()

	var deregisterErr error
	if registry != nil && service != nil {
		deregisterErr = deregister(ctx, registry, service)
		if deregisterErr != nil {
			deregisterErr = exception.WrapCore(deregisterErr, "注销 gRPC 服务发现记录失败")
		}
	}
	var shutdownErr error
	if state == stateStarted {
		gracefulDone := make(chan struct{})
		go func() {
			server.Server.GracefulStop()
			close(gracefulDone)
		}()
		select {
		case <-gracefulDone:
		case <-ctx.Done():
			server.Server.Stop()
			shutdownErr = ctx.Err()
			<-gracefulDone
		}
	}
	closeErr := closeListener(listener)

	transport.mu.Lock()
	transport.listener = nil
	transport.preparedService = nil
	transport.registry = nil
	transport.state = stateStopped
	transport.mu.Unlock()

	return errors.Join(deregisterErr, shutdownErr, closeErr)
}

func deregister(ctx context.Context, registry gsvc.Registrar, service gsvc.Service) error {
	result := make(chan error, 1)
	go func() { result <- registry.Deregister(ctx, service) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (transport *Transport) failStart(ctx context.Context, cause error) error {
	return errors.Join(cause, transport.Stop(ctx))
}

func (transport *Transport) listenAddress() string {
	return net.JoinHostPort(transport.config.Address, strconv.Itoa(transport.config.Port))
}

func (transport *Transport) terminate(err error) {
	transport.terminationOnce.Do(func() {
		transport.terminated <- err
		close(transport.terminated)
	})
}

func (transport *Transport) closeTermination() {
	transport.terminationOnce.Do(func() { close(transport.terminated) })
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}

	return err
}

func newService(name, address string) gsvc.Service {
	return &gsvc.LocalService{
		Name:      name,
		Endpoints: gsvc.Endpoints{gsvc.NewEndpoint(address)},
		Metadata:  gsvc.Metadata{gsvc.MDProtocol: "grpc"},
	}
}

func unaryReady(readiness app.ReadyState) grpcstd.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpcstd.UnaryServerInfo, handler grpcstd.UnaryHandler) (any, error) {
		if readiness == nil || !readiness.Ready() {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		}
		return handler(ctx, request)
	}
}

func streamReady(readiness app.ReadyState) grpcstd.StreamServerInterceptor {
	return func(service any, stream grpcstd.ServerStream, info *grpcstd.StreamServerInfo, handler grpcstd.StreamHandler) error {
		if readiness == nil || !readiness.Ready() {
			return status.Error(codes.Unavailable, "service unavailable")
		}
		return handler(service, stream)
	}
}

func unaryError(
	ctx context.Context,
	request any,
	info *grpcstd.UnaryServerInfo,
	handler grpcstd.UnaryHandler,
) (any, error) {
	response, err := handler(ctx, request)
	return response, Error(err)
}

func streamError(service any, stream grpcstd.ServerStream, info *grpcstd.StreamServerInfo, handler grpcstd.StreamHandler) error {
	return Error(handler(service, stream))
}
