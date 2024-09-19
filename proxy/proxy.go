package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
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

	//copy response
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	//here we close the done
	close(done)

}
