package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"radio-stream-manager/internal/config"
	"radio-stream-manager/internal/events"
	"radio-stream-manager/internal/processes"
	"radio-stream-manager/internal/state"

	"go.uber.org/zap"
)

func main() {
	logLevel := os.Getenv("LOG_LEVEL")
	var cfgZap zap.Config
	if logLevel == "debug" {
		cfgZap = zap.NewDevelopmentConfig()
	} else {
		cfgZap = zap.NewProductionConfig()
	}
	if logLevel != "" {
		if err := cfgZap.Level.UnmarshalText([]byte(logLevel)); err != nil {
			log.Printf("Invalid LOG_LEVEL: %v, defaulting to %v", logLevel, cfgZap.Level)
		}
	}
	logger, err := cfgZap.Build()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateManager, err := state.NewDynamoDBManager(cfg.AWS.Region, cfg.AWS.DynamoDBTable)
	if err != nil {
		logger.Fatal("Failed to create state manager", zap.Error(err))
	}

	processManager := processes.NewManager(cfg, stateManager, logger)

	// Restore previously running streams after startup
	if err := processManager.RestoreStreams(ctx); err != nil {
		logger.Error("Failed to restore streams", zap.Error(err))
	}

	eventConsumer, err := events.NewSQSConsumer(cfg, processManager, logger)
	if err != nil {
		logger.Fatal("Failed to create event consumer", zap.Error(err))
	}

	logger.Info("Starting radio stream manager",
		zap.String("version", "1.0.0"),
		zap.String("queue_url", cfg.AWS.SQSQueueURL))

	go func() {
		if err := eventConsumer.Start(ctx); err != nil {
			logger.Error("Event consumer error", zap.Error(err))
			cancel()
		}
	}()

	go func() {
		processManager.StartHealthMonitor(ctx)
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
	case <-ctx.Done():
		logger.Info("Context cancelled")
	}

	logger.Info("Shutting down gracefully...")

	if err := processManager.StopAll(); err != nil {
		logger.Error("Error stopping processes", zap.Error(err))
	}

	logger.Info("Shutdown complete")
}
