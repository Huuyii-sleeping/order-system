// Package diagnostics 提供与业务 HTTP 端口隔离的 Go 运行时诊断服务。
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	Address              string
	MutexProfileFraction int
	BlockProfileRate     int
}

// Server 持有已经绑定的回环 Listener。构造阶段先占用端口，可以让地址冲突
// 在业务 HTTP Server 开始接收流量前失败，而不是留下一个缺少诊断能力的半健康实例。
type Server struct {
	listener             net.Listener
	httpServer           *http.Server
	mutexProfileFraction int
	blockProfileRate     int
}

func New(config Config) (*Server, error) {
	address := strings.TrimSpace(config.Address)
	if err := validateLoopbackAddress(address); err != nil {
		return nil, err
	}
	if config.MutexProfileFraction < 0 {
		return nil, errors.New("mutex profile fraction must not be negative")
	}
	if config.BlockProfileRate < 0 {
		return nil, errors.New("block profile rate must not be negative")
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on diagnostics address %q: %w", address, err)
	}

	return &Server{
		listener: listener,
		httpServer: &http.Server{
			Handler:           NewHandler(),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       30 * time.Second,
		},
		mutexProfileFraction: config.MutexProfileFraction,
		blockProfileRate:     config.BlockProfileRate,
	}, nil
}

// NewHandler 显式注册 pprof Handler，不使用 http.DefaultServeMux。
// 这样业务 API 即使意外导入本包，也不会自动出现 /debug/pprof 路由。
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", httppprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", httppprof.Trace)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(writer, request)
	})
}

func (s *Server) Address() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// RunGracefully 把停止接收新请求和强制取消在途 Profile 分成两阶段。
// stopCtx 触发 http.Server.Shutdown；如果 workCtx 的关闭预算耗尽，Close
// 会强制中断仍在执行的 CPU Profile 或 Trace。
func (s *Server) RunGracefully(stopCtx, workCtx context.Context) error {
	if s == nil || s.listener == nil || s.httpServer == nil {
		return errors.New("diagnostics server is not initialized")
	}
	if stopCtx == nil || workCtx == nil {
		return errors.New("diagnostics contexts must not be nil")
	}

	previousMutexFraction := runtime.SetMutexProfileFraction(s.mutexProfileFraction)
	runtime.SetBlockProfileRate(s.blockProfileRate)
	defer func() {
		runtime.SetMutexProfileFraction(previousMutexFraction)
		runtime.SetBlockProfileRate(0)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- s.httpServer.Serve(s.listener)
	}()

	select {
	case err := <-serverErrors:
		return normalizeServerError(err)
	case <-stopCtx.Done():
	}

	if err := s.httpServer.Shutdown(workCtx); err != nil {
		closeErr := s.httpServer.Close()
		return errors.Join(
			fmt.Errorf("shutdown diagnostics server: %w", err),
			normalizeServerError(closeErr),
		)
	}
	return normalizeServerError(<-serverErrors)
}

// Close 处理 Server 已构造但尚未交给后台 Runtime 时的启动失败路径。
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var closeErrors []error
	if s.httpServer != nil {
		closeErrors = append(closeErrors, normalizeServerError(s.httpServer.Close()))
	}
	if s.listener != nil {
		closeErrors = append(closeErrors, normalizeServerError(s.listener.Close()))
	}
	return errors.Join(closeErrors...)
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse diagnostics address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("diagnostics address %q must use an explicit loopback IP", address)
	}
	return nil
}

func normalizeServerError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
