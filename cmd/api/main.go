package main

import (
	"context"
	payments "entradasya.local/payments"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := payments.ConfigFromEnv()
	if err != nil {
		logger.Error("invalid_configuration")
		os.Exit(1)
	}
	service, err := payments.New(config, nil)
	if err != nil {
		logger.Error("initialization_failed")
		os.Exit(1)
	}
	server := &http.Server{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port)), Handler: service.Handler(logger), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("payments_starting", "port", config.Port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server_failed")
		os.Exit(1)
	}
}
