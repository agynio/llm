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

	agentsv1 "github.com/agynio/llm/.gen/go/agynio/api/agents/v1"
	authorizationv1 "github.com/agynio/llm/.gen/go/agynio/api/authorization/v1"
	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	notificationsv1 "github.com/agynio/llm/.gen/go/agynio/api/notifications/v1"
	secretsv1 "github.com/agynio/llm/.gen/go/agynio/api/secrets/v1"
	"github.com/agynio/llm/internal/config"
	"github.com/agynio/llm/internal/db"
	"github.com/agynio/llm/internal/grpcserver"
	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/agynio/llm/internal/subscription"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	authorizationConn, err := grpc.DialContext(ctx, cfg.AuthorizationAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial authorization: %w", err)
	}
	defer authorizationConn.Close()

	secretsConn, err := grpc.DialContext(ctx, cfg.SecretsAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial secrets: %w", err)
	}
	defer secretsConn.Close()
	agentsConn, err := grpc.DialContext(ctx, cfg.AgentsAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial agents: %w", err)
	}
	defer agentsConn.Close()
	notificationsConn, err := grpc.DialContext(ctx, cfg.NotificationsAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial notifications: %w", err)
	}
	defer notificationsConn.Close()

	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("agynio.api.llm.v1.LLMService", healthpb.HealthCheckResponse_SERVING)
	llmServer := grpcserver.New(providerStore, modelStore, authorizationv1.NewAuthorizationServiceClient(authorizationConn), http.DefaultClient).
		WithSubscriptions(grpcserver.SubscriptionDeps{
			Store:         subscription.NewStore(pool),
			Secrets:       secretsv1.NewSecretsServiceClient(secretsConn),
			Agents:        agentsv1.NewAgentsServiceClient(agentsConn),
			Notifications: notificationsv1.NewNotificationsServiceClient(notificationsConn),
		})
	llmv1.RegisterLLMServiceServer(grpcServer, llmServer)

	grpcListener, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GRPCAddress, err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("LLM gRPC listening on %s", cfg.GRPCAddress)
		if err := grpcServer.Serve(grpcListener); err != nil {
			if errors.Is(err, grpc.ErrServerStopped) {
				return
			}
			errCh <- fmt.Errorf("serve grpc: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

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
