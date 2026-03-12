package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	llmv1 "github.com/agynio/llm/gen/go/agynio/api/llm/v1"
	"github.com/agynio/llm/internal/config"
	"github.com/agynio/llm/internal/db"
	"github.com/agynio/llm/internal/grpcserver"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/agynio/llm/internal/proxy"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("llm: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("create connection pool: %w", err)
	}
	defer pool.Close()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	providerStore := provider.NewStore(pool)
	modelStore := model.NewStore(pool)
	resolver := proxy.NewResolver(providerStore, modelStore)
	proxyService := proxy.NewService(resolver, &http.Client{})

	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("agynio.api.llm.v1.LLMService", healthpb.HealthCheckResponse_SERVING)
	llmv1.RegisterLLMServiceServer(grpcServer, grpcserver.New(providerStore, modelStore, proxyService))

	grpcListener, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GRPCAddress, err)
	}

	httpServer := &http.Server{
		Addr:    cfg.HTTPAddress,
		Handler: proxy.NewHandler(proxyService),
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("LLM gRPC listening on %s", cfg.GRPCAddress)
		if err := grpcServer.Serve(grpcListener); err != nil {
			if errors.Is(err, grpc.ErrServerStopped) {
				return
			}
			errCh <- fmt.Errorf("serve grpc: %w", err)
		}
	}()

	go func() {
		log.Printf("LLM HTTP listening on %s", cfg.HTTPAddress)
		if err := httpServer.ListenAndServe(); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				return
			}
			errCh <- fmt.Errorf("serve http: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http: %w", err)
	}

	healthServer.Shutdown()
	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	return nil
}
