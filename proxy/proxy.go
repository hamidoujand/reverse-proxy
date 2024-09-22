package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// Proxy represents the proxy handler.
type Proxy struct {
	Host   *url.URL //right now one hardcoded host.
	Client *http.Client
}

func New(host string) (*Proxy, error) {
	var p Proxy
	var err error

	p.Host, err = url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	//client
	p.Client = &http.Client{
		Timeout: time.Second * 5, // total request timeout.
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: time.Second, //dial timeout
			}).DialContext,
			TLSHandshakeTimeout:   time.Second,
			ResponseHeaderTimeout: time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //TODO: only for tests
			},
		},
	}

	return &p, nil
}

// ServeHTTP implements the http handler interface.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//forwarding
	r.Host = p.Host.Host
	r.URL.Host = p.Host.Host
	r.URL.Scheme = p.Host.Scheme
	r.RequestURI = ""
	//set X-FORWARDED-FOR
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		//internal
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, err)
		return
	}
	r.Header.Set("X-Forwarded-For", ip)

	if r.ProtoMajor == 2 {
		//add http2 support
		if err := http2.ConfigureTransport(p.Client.Transport.(*http.Transport)); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, err)
			return
		}
	}

	//client
	resp, err := p.Client.Do(r)
	if err != nil {
		//internal error
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, err)
		return
	}
	//copy headers
	for header, values := range resp.Header {
		for _, val := range values {
			w.Header().Set(header, val)
		}
	}

	//handle stream
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-time.Tick(time.Millisecond * 10):
				w.(http.Flusher).Flush()
			case <-done:
				return
			}
		}
	}()

	//handle trailers
	trailerKeys := make([]string, 0, len(resp.Trailer))
	for key := range resp.Trailer {
		trailerKeys = append(trailerKeys, key)
	}

	//anounce the trailers
	w.Header().Set("Trailer", strings.Join(trailerKeys, ","))

	//copy response
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	//fill the trailer values
	for key, values := range resp.Trailer {
		for _, val := range values {
			w.Header().Set(key, val)
		}
	}

	//here we close the done
	close(done)
}
