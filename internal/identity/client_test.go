package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientUsesRotatingCertificateAndEndpoints(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "instance.crt")
	keyPath := filepath.Join(dir, "instance.key")
	pool, serverCert, clientCert := certificates(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Error("missing client certificate")
		}
		if r.URL.Path != "/bootstrap/health" && r.URL.Path != "/bootstrap/join" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	writePair(t, certPath, keyPath, clientCert)
	host := strings.TrimPrefix(server.URL, "https://")
	client := New(Config{CertPath: certPath, KeyPath: keyPath, RootCAs: pool, Timeout: time.Second, MaxBodyBytes: 1024})
	ok, err := client.Reachable(context.Background(), host)
	if err != nil || !ok {
		t.Fatalf("Reachable = %v, %v", ok, err)
	}
	op, err := client.TriggerEnrollment(context.Background(), host)
	if err != nil || !op.Success {
		t.Fatalf("TriggerEnrollment = %#v, %v", op, err)
	}
	os.Remove(certPath)
	if _, err := client.Reachable(context.Background(), host); err == nil {
		t.Fatal("missing rotated certificate did not fail closed")
	}
}

func TestClientRejectsUnsafeHostAndBoundsResponse(t *testing.T) {
	client := New(Config{CertPath: "missing", KeyPath: "missing", Timeout: time.Second, MaxBodyBytes: 16})
	for _, host := range []string{"", "https://example.com", "example.com/path", "user@example.com"} {
		if _, err := client.Reachable(context.Background(), host); err == nil {
			t.Fatalf("host %q accepted", host)
		}
	}
}

func certificates(t *testing.T) (*x509.CertPool, tls.Certificate, tls.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return pool, pair, pair
}

func writePair(t *testing.T, certPath, keyPath string, pair tls.Certificate) {
	t.Helper()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	key := pair.PrivateKey.(*rsa.PrivateKey)
	keyBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certPath, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyBytes, 0600); err != nil {
		t.Fatal(err)
	}
}
