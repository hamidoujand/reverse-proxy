package proxy_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hamidoujand/reverse-proxy/proxy"
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
