// Command extproc runs the compliance ext_proc gRPC service.
//
// It is meant to be wired into an Envoy-based Gateway-API data plane (Envoy
// Gateway) via an EnvoyExtensionPolicy. On the request path it tokenizes PII
// detected by Presidio; on the response path it re-hydrates those tokens.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/coreoptimizer/promptcloak/internal/config"
	"github.com/coreoptimizer/promptcloak/internal/detect"
	"github.com/coreoptimizer/promptcloak/internal/extproc"
	"github.com/coreoptimizer/promptcloak/internal/redact"
	"github.com/coreoptimizer/promptcloak/internal/vault"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	v, err := buildVault(ctx, cfg, logger)
	if err != nil {
		logger.Error("init vault", "error", err)
		os.Exit(1)
	}
	defer v.Close()

	analyzer := detect.NewPresidio(cfg.Presidio.URL, cfg.Presidio.ScoreThreshold, cfg.Presidio.Entities, cfg.Presidio.Timeout)
	redactor := redact.New(analyzer, v, cfg.Presidio.Language, cfg.TokenSalt)
	srv := extproc.NewServer(redactor, v, cfg.FailOpen, logger)

	grpcServer := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(grpcServer, srv)

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Error("listen", "addr", cfg.ListenAddr, "error", err)
		os.Exit(1)
	}

	httpSrv := startHealthServer(cfg.HealthAddr, logger)

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		grpcServer.GracefulStop()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	logger.Info("promptcloak ext_proc listening",
		"grpc", cfg.ListenAddr,
		"health", cfg.HealthAddr,
		"presidio", cfg.Presidio.URL,
		"vault", vaultKind(cfg),
		"fail_open", cfg.FailOpen,
	)
	if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		logger.Error("grpc serve", "error", err)
		os.Exit(1)
	}
}

func buildVault(ctx context.Context, cfg *config.Config, logger *slog.Logger) (vault.Vault, error) {
	if cfg.Redis.Addr == "" {
		logger.Warn("REDIS_ADDR not set: using in-memory vault (single-replica, non-durable) — not for production")
		return vault.NewMemory(cfg.TokenTTL), nil
	}
	return vault.NewRedis(ctx, vault.RedisDial{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}, cfg.TokenTTL)
}

func vaultKind(cfg *config.Config) string {
	if cfg.Redis.Addr == "" {
		return "memory"
	}
	return "redis"
}

// startHealthServer serves liveness/readiness probes for Kubernetes.
func startHealthServer(addr string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server", "error", err)
		}
	}()
	return srv
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
