package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hamidoujand/reverse-proxy/proxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	host := os.Getenv("HOST")
	if host == "" {
		return errors.New("HOST is required environment variable")
	}
	readTimeoutSTR := os.Getenv("READ_TIMEOUT")
	if readTimeoutSTR == "" {
		readTimeoutSTR = "5s"
	}

	readTimeout, err := time.ParseDuration(readTimeoutSTR)
	if err != nil {
		return fmt.Errorf("%s is not a valid duration: %w", readTimeoutSTR, err)
	}

	writeTimeoutSTR := os.Getenv("WRITE_TIMEOUT")
	if writeTimeoutSTR == "" {
		writeTimeoutSTR = "10s"
	}

	writeTimeout, err := time.ParseDuration(writeTimeoutSTR)
	if err != nil {
		return fmt.Errorf("%s is not a valid duration: %w", writeTimeoutSTR, err)
	}

	shutdownTimeoutSTR := os.Getenv("SHUTDOWN_TIMEOUT")
	if shutdownTimeoutSTR == "" {
		shutdownTimeoutSTR = "20s"
	}

	shutdownTimeout, err := time.ParseDuration(shutdownTimeoutSTR)
	if err != nil {
		return fmt.Errorf("%s is not a valid duration: %w", shutdownTimeoutSTR, err)
	}
	//==============================================================================
	//Server
	proxy, err := proxy.New("http://127.0.0.1:9000")

	if err != nil {
		return fmt.Errorf("new proxy handler: %w", err)
	}

	server := http.Server{
		Addr:        host,
		Handler:     http.TimeoutHandler(proxy, writeTimeout, "timed out"),
		ReadTimeout: readTimeout,
		ErrorLog:    log.Default(),
	}

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	serverErrs := make(chan error, 1)

	go func() {
		log.Printf("proxy server running on: %s\n", host)
		if err := server.ListenAndServe(); err != nil {
			serverErrs <- err
		}
	}()

	select {
	case err := <-serverErrs:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdownCh:
		log.Printf("received %s, shutting down\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			server.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
	}
	return nil
}
