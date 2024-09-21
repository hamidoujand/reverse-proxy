package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
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
	//==========================================================================
	//TLS Support

	//generate private key
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}
	now := time.Now()
	then := now.Add(time.Hour * 24 * 365) //one year later
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "reverse.proxy.name",
			Organization: []string{"Reverse-Proxy"},
		},
		NotBefore:             now,
		NotAfter:              then,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &private.PublicKey, private)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	certFile, err := os.Create("certificate.cer")
	if err != nil {
		return fmt.Errorf("create cert file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("encode into pem: %w", err)
	}

	privateFile, err := os.Create("private.pem")
	if err != nil {
		return fmt.Errorf("create private file: %w", err)
	}

	defer privateFile.Close()

	if err := pem.Encode(privateFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}); err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}

	//==========================================================================
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
		if err := server.ListenAndServeTLS("certificate.cer", "private.pem"); err != nil {
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
