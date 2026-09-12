package gnhttp

import (
	"context"
	"errors"
	"net"
	stdhttp "net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/app"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const statusPollInterval = 5 * time.Millisecond

var serverSequence atomic.Uint64

// 生成路由与中间件安装函数
type Installer func(*ghttp.Server) error

// 静态文件目录注册器
type StaticFileInstaller interface {
	InstallStaticFiles(*ghttp.Server) error
}

// GoFrame HTTP 承载适配器
type Transport struct {
	config     Config
	install    Installer
	listener   net.Listener
	mu         sync.Mutex
	newRuntime func() serverRuntime
	runtime    serverRuntime
	state      transportState
	stopDone   chan struct{}
	stopErr    error
	stopOnce   sync.Once
	terminated chan error
}

type transportState uint8

const (
	stateNew transportState = iota
	statePrepared
	stateStarting
	stateStarted
	stateStopping
	stateStopped
)

type serverRuntime interface {
	Configure(app.ReadyState, Installer) error
	SetListener(net.Listener) error
	Start() error
	Status() int
	Shutdown() error
}

type goFrameRuntime struct {
	server *ghttp.Server
}

// 创建 HTTP Transport
func New(config Config, install Installer) (*Transport, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if install == nil {
		return nil, exception.Core("HTTP Installer 不能为空")
	}
	configuredInstall := func(server *ghttp.Server) error {
		server.SetClientMaxBodySize(config.ClientMaxBodySize)

		return install(server)
	}

	return newTransport(config, configuredInstall, newGoFrameRuntime), nil
}

// 返回固定 Transport 名称
func (transport *Transport) Name() string { return "http" }

// 返回 HTTP 是否启用
func (transport *Transport) Enabled() bool {
	return transport != nil && transport.config.Enabled
}

// 预安装路由并绑定监听端口
func (transport *Transport) Prepare(ctx context.Context) error {
	if transport == nil {
		return exception.Core("HTTP Transport 未初始化")
	}
	if !transport.config.Enabled {
		return exception.Core("HTTP Transport 未启用")
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
		return exception.Core("HTTP Transport 已准备或停止")
	}
	runtime := transport.newRuntime()
	if runtime == nil {
		return exception.Core("HTTP Server 未初始化")
	}
	if err := runtime.Configure(app.Readiness(ctx), transport.install); err != nil {
		return exception.WrapCore(err, "安装 HTTP 路由与中间件失败")
	}
	listener, err := net.Listen("tcp", transport.listenAddress())
	if err != nil {
		return exception.WrapCore(err, "绑定 HTTP 监听地址失败")
	}
	if err = runtime.SetListener(listener); err != nil {
		_ = listener.Close()
		return exception.WrapCore(err, "设置 HTTP Listener 失败")
	}
	transport.runtime = runtime
	transport.listener = listener
	transport.state = statePrepared

	return nil
}

// 启动 GoFrame 并等待服务循环运行
func (transport *Transport) Start(ctx context.Context) (<-chan error, error) {
	if transport == nil {
		return nil, exception.Core("HTTP Transport 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	transport.mu.Lock()
	if transport.state != statePrepared {
		transport.mu.Unlock()
		return nil, exception.Core("HTTP Transport 尚未准备或已启动")
	}
	transport.state = stateStarting
	runtime := transport.runtime
	transport.mu.Unlock()

	if err := runtime.Start(); err != nil {
		transport.mu.Lock()
		if transport.state == stateStarting {
			transport.state = statePrepared
		}
		transport.mu.Unlock()
		return nil, transport.failStart(ctx, exception.WrapCore(err, "启动 GoFrame HTTP Server 失败"))
	}
	waitCtx, cancel := context.WithTimeout(ctx, transport.config.StartTimeout)
	defer cancel()
	ticker := time.NewTicker(statusPollInterval)
	defer ticker.Stop()
	for {
		if runtime.Status() == ghttp.ServerStatusRunning {
			transport.mu.Lock()
			if transport.state == stateStarting {
				transport.state = stateStarted
				transport.mu.Unlock()
				return transport.terminated, nil
			}
			transport.mu.Unlock()
			return nil, transport.failStart(ctx, exception.Core("HTTP Transport 在启动期间停止"))
		}
		select {
		case <-waitCtx.Done():
			return nil, transport.failStart(waitCtx, exception.WrapCore(waitCtx.Err(), "等待 GoFrame HTTP Server 运行失败"))
		case <-ticker.C:
		}
	}
}

// 停止接收请求并释放自持 Listener
func (transport *Transport) Stop(ctx context.Context) error {
	if transport == nil {
		return nil
	}
	transport.stopOnce.Do(func() {
		transport.stopErr = transport.stop(ctx)
		close(transport.terminated)
		close(transport.stopDone)
	})
	<-transport.stopDone

	return transport.stopErr
}

func newTransport(config Config, install Installer, factory func() serverRuntime) *Transport {
	return &Transport{
		config:     config,
		install:    install,
		newRuntime: factory,
		stopDone:   make(chan struct{}),
		terminated: make(chan error),
	}
}

func newGoFrameRuntime() serverRuntime {
	name := "cool-http-" + strconv.FormatUint(serverSequence.Add(1), 10)

	return &goFrameRuntime{server: ghttp.GetServer(name)}
}

// Ready Gate 与路由中间件
func (runtime *goFrameRuntime) Configure(readiness app.ReadyState, install Installer) error {
	runtime.server.Use(readyGate(readiness))

	return install(runtime.server)
}

// 外部监听器
func (runtime *goFrameRuntime) SetListener(listener net.Listener) error {
	return runtime.server.SetListener(listener)
}

// HTTP 服务
func (runtime *goFrameRuntime) Start() error { return runtime.server.Start() }

// 底层服务状态码
func (runtime *goFrameRuntime) Status() int { return runtime.server.Status() }

// HTTP 服务并释放监听器
func (runtime *goFrameRuntime) Shutdown() error { return runtime.server.Shutdown() }

func readyGate(readiness app.ReadyState) ghttp.HandlerFunc {
	return func(request *ghttp.Request) {
		if readiness == nil || !readiness.Ready() {
			request.Response.WriteStatus(stdhttp.StatusServiceUnavailable)
			request.ExitAll()
			return
		}
		request.Middleware.Next()
	}
}

func (transport *Transport) listenAddress() string {
	return net.JoinHostPort(transport.config.Address, strconv.Itoa(transport.config.Port))
}

func (transport *Transport) failStart(ctx context.Context, cause error) error {
	return errors.Join(cause, transport.Stop(ctx))
}

func (transport *Transport) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	transport.mu.Lock()
	state := transport.state
	listener := transport.listener
	runtime := transport.runtime
	if state == stateNew || state == stateStopped {
		transport.state = stateStopped
		transport.mu.Unlock()
		return nil
	}
	transport.state = stateStopping
	transport.mu.Unlock()

	var shutdownErr error
	if state == stateStarting || state == stateStarted {
		result := make(chan error, 1)
		go func() { result <- runtime.Shutdown() }()
		if err := ctx.Err(); err != nil {
			shutdownErr = err
		} else {
			select {
			case shutdownErr = <-result:
			case <-ctx.Done():
				shutdownErr = ctx.Err()
			}
		}
	}
	closeErr := closeListener(listener)

	transport.mu.Lock()
	transport.listener = nil
	transport.state = stateStopped
	transport.mu.Unlock()

	return errors.Join(normalizeClosed(shutdownErr), closeErr)
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}

	return normalizeClosed(listener.Close())
}

func normalizeClosed(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}

	return err
}
