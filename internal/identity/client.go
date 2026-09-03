package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cf-herdr-poc/internal/model"
)

type Config struct {
	CertPath     string
	KeyPath      string
	RootCAs      *x509.CertPool
	Timeout      time.Duration
	MaxBodyBytes int64
}
type Client struct{ config Config }

func New(config Config) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 64 << 10
	}
	return &Client{config: config}
}
func (c *Client) Reachable(ctx context.Context, host string) (bool, error) {
	response, err := c.do(ctx, http.MethodGet, host, "/bootstrap/health")
	if err != nil {
		return false, err
	}
	return response == http.StatusOK || response == http.StatusNoContent, nil
}
func (c *Client) TriggerEnrollment(ctx context.Context, host string) (model.Operation, error) {
	started := time.Now()
	status, err := c.do(ctx, http.MethodPost, host, "/bootstrap/join")
	op := model.Operation{Name: "trigger-enrollment", StartedAt: started, Duration: time.Since(started), Success: err == nil && status >= 200 && status < 300}
	if err != nil {
		op.Error = "identity enrollment request failed"
		return op, err
	}
	if !op.Success {
		err = fmt.Errorf("identity enrollment returned status %d", status)
		op.Error = err.Error()
	}
	return op, err
}
func (c *Client) do(ctx context.Context, method, host, path string) (int, error) {
	if !validHost(host) {
		return 0, errors.New("invalid identity host")
	}
	certificate, err := tls.LoadX509KeyPair(c.config.CertPath, c.config.KeyPath)
	if err != nil {
		return 0, fmt.Errorf("load instance identity: %w", err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, RootCAs: c.config.RootCAs, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.config.Timeout}
	request, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, c.config.MaxBodyBytes+1)); err != nil {
		return 0, err
	}
	return response.StatusCode, nil
}
func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/@?#") {
		return false
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host {
		return false
	}
	hostname := parsed.Hostname()
	return hostname != "" && (net.ParseIP(hostname) != nil || !strings.Contains(hostname, " "))
}
