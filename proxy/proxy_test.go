package proxy_test

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hamidoujand/reverse-proxy/proxy"
	"golang.org/x/net/http2"
)

func TestNewProxyHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	//add headers
	req.Header.Add("Test-Header-1", "1")
	req.Header.Add("Test-Header-2", "2")

	msg := "Hello World!"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//check headers
		header1 := r.Header.Get("Test-Header-1")
		header2 := r.Header.Get("Test-Header-2")
		if header1 != "1" {
			t.Errorf("header1=%s, got %s", "1", header1)
		}

		if header2 != "2" {
			t.Errorf("header2=%s, got %s", "2", header2)
		}

		//check "X-Forwarded-For" header
		xForwarded := r.Header.Get("X-Forwarded-For")
		reqIP, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			t.Fatalf("failed to split host and port from remote addr: %s", err)
		}

		if xForwarded != reqIP {
			t.Errorf("X-Forwarded-For = %s, got %s", reqIP, xForwarded)
		}

		t.Logf("server was hit from %s\n", r.RemoteAddr)

		//announce your trailers
		w.Header().Set("Trailer", "X-Trailer")

		//write some response headers
		w.Header().Set("Response-Header-1", "1")

		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, msg)

		//write the trailers
		w.Header().Set("X-Trailer", "X-Value")
	}))
	defer server.Close()

	p, err := proxy.New(server.URL)
	if err != nil {
		t.Fatalf("failed to create proxy handler: %s", err)
	}

	host := strings.TrimPrefix(server.URL, "http://")
	if host != p.Host.Host {
		t.Fatalf("host=%s, got %s", host, p.Host.Host)
	}

	recorder := httptest.NewRecorder()

	p.ServeHTTP(recorder, req)

	if recorder.Result().StatusCode != http.StatusCreated {
		t.Errorf("status=%d, got %d", http.StatusCreated, recorder.Result().StatusCode)
	}

	//get the response
	bs, err := io.ReadAll(recorder.Body)
	if err != nil {
		t.Fatalf("failed to read all response body: %s", err)
	}

	if string(bs) != msg {
		t.Errorf("message=%s, got %s", msg, string(bs))
	}

	//check for response header
	responseHeader := recorder.Header().Get("Response-Header-1")
	if responseHeader != "1" {
		t.Errorf("responseHeader=%s, got %s", "1", responseHeader)
	}

	//after body was read, check the trailers
	trailerHeader := recorder.Result().Trailer.Get("X-Trailer")
	expectedTrailer := "X-Value"
	if trailerHeader != expectedTrailer {
		t.Errorf("Trailer=%s, got %s", expectedTrailer, trailerHeader)
	}
}

func TestProxyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, "server not supporting stream")
			return
		}

		w.Header().Set("Content-Type", "text/pain")
		for i := range 3 {
			fmt.Fprintf(w, "chunk#%d", i+1)
			flusher.Flush()
			time.Sleep(time.Millisecond * 500)
		}
	}))

	defer server.Close()

	client := &http.Client{}

	p, err := proxy.New(server.URL)
	if err != nil {
		t.Fatalf("failed to create a proxy: %s", err)
	}

	//create proxy server
	proxyServer := httptest.NewServer(p)
	defer proxyServer.Close()

	resp, err := client.Get(proxyServer.URL) //clinet --> proxyServer ---> server
	if err != nil {
		t.Fatalf("failed to make request to proxy server: %s", err)
	}

	defer resp.Body.Close()

	buf := make([]byte, 20)
	chunkCount := 0

	for {
		n, err := resp.Body.Read(buf)

		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("expected to read response body in chunks: %s", err)
		}

		t.Logf("Received: %s\n", buf[:n])
		chunkCount++
	}

	expectedChunkCount := 3
	if expectedChunkCount != chunkCount {
		t.Fatalf("chunkCount=%d, got %d", expectedChunkCount, chunkCount)
	}
}

func TestHTTP2Proxy(t *testing.T) {
	// Create an HTTP/2 server that will be the upstream server
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Log("Upstream server hit")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "Hello World!")
	}))

	// Configure server for HTTP/2
	server.TLS = &tls.Config{
		NextProtos: []string{http2.NextProtoTLS}, // server supports HTTP/2
	}

	if err := http2.ConfigureServer(server.Config, &http2.Server{}); err != nil {
		t.Fatalf("failed to configure http2 server: %s", err)
	}

	server.StartTLS()
	defer server.Close()

	// Create the reverse proxy pointing to the upstream server
	p, err := proxy.New(server.URL) // Assuming proxy.New creates a reverse proxy
	if err != nil {
		t.Fatalf("failed to create new proxy server: %s", err)
	}

	// Create an HTTP/2 proxy server (this will forward requests to the upstream server)
	proxyServer := httptest.NewUnstartedServer(p)
	proxyServer.TLS = &tls.Config{
		NextProtos: []string{http2.NextProtoTLS, "http/1.1"}, // Supports both HTTP/2 and HTTP/1.1
	}

	proxyServer.StartTLS() // Start the proxy server with TLS
	defer proxyServer.Close()

	// Create an HTTP client that can talk to the proxy server
	client := &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Skip certificate verification for the test
			},
		},
	}

	// Create a new GET request to the proxy server
	req, err := http.NewRequest(http.MethodGet, proxyServer.URL, nil)
	if err != nil {
		t.Fatalf("failed to create a new request: %s", err)
	}

	// Make the request to the proxy server
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to do the request to proxy server: %s", err)
	}
	defer resp.Body.Close()

	// Check the response status
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("statusCode=%d, got %d", http.StatusCreated, resp.StatusCode)
	}

	// Optional: read and check the response body if needed
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %s", err)
	}

	expectedBody := "Hello World!"
	if string(body) != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, string(body))
	}
}

// func TestHTTP2Proxy(t *testing.T) {
// 	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		t.Log("tls server was hit")
// 		w.WriteHeader(http.StatusCreated)
// 		fmt.Fprint(w, "Hello World!")
// 	}))

// 	server.TLS = &tls.Config{
// 		NextProtos: []string{http2.NextProtoTLS}, //server supports HTTP2
// 	}

// 	if err := http2.ConfigureServer(server.Config, &http2.Server{}); err != nil {
// 		t.Fatalf("failed to configure http2 server: %s", err)
// 	}

// 	server.StartTLS()
// 	defer server.Close()

// 	p, err := proxy.New(server.URL)
// 	if err != nil {
// 		t.Fatalf("failed to create new proxy server: %s", err)
// 	}

// 	proxyServer := httptest.NewUnstartedServer(p)
// 	proxyServer.TLS = &tls.Config{
// 		NextProtos: []string{http2.NextProtoTLS, "http/1.1"},
// 	}

// 	proxyServer.StartTLS()
// 	defer proxyServer.Close()

// 	proxyCert := proxyServer.TLS.Certificates[0].Certificate[0]
// 	rootCAs := x509.NewCertPool()
// 	rootCAs.AppendCertsFromPEM(proxyCert)

// 	client := http.Client{
// 		Transport: &http2.Transport{
// 			TLSClientConfig: &tls.Config{
// 				RootCAs: rootCAs, //trust this dummy CA from test server.
// 			},
// 		},
// 	}

// 	req, err := http.NewRequest(http.MethodGet, proxyServer.URL, nil)
// 	if err != nil {
// 		t.Fatalf("failed to create a new request: %s", err)
// 	}

// 	resp, err := client.Do(req)
// 	if err != nil {
// 		t.Fatalf("failed to do the request to proxy server: %s", err)
// 	}

// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusCreated {
// 		t.Errorf("statusCode=%d, got %d", http.StatusCreated, resp.StatusCode)
// 	}
// }
