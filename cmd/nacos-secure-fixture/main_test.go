package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateMaterialProducesSeparateServerAndClientIdentities(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tls")
	now := time.Now().UTC().Truncate(time.Second)
	if err := generateMaterial(directory, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ca.pem", "server.pem", "server-key.pem", "client.pem", "client-key.pem", "wrong-ca.pem"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("generated %s info=%v err=%v", name, info, err)
		}
	}
	server, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	serverLeaf, err := x509.ParseCertificate(server.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := serverLeaf.VerifyHostname("nacos.internal"); err != nil {
		t.Fatal(err)
	}
	client, err := tls.LoadX509KeyPair(filepath.Join(directory, "client.pem"), filepath.Join(directory, "client-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	clientLeaf, err := x509.ParseCertificate(client.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(clientLeaf.ExtKeyUsage) != 1 || clientLeaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("client usages=%v", clientLeaf.ExtKeyUsage)
	}
}

func TestGenerateMaterialRejectsReuse(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tls")
	if err := generateMaterial(directory, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := generateMaterial(directory, time.Now()); err == nil {
		t.Fatal("second generation unexpectedly replaced existing credentials")
	}
}

func TestSecureHTTPProxyRequiresClientIdentityAndForwards(t *testing.T) {
	directory := generateTestMaterial(t)
	serverIdentity, pool, err := loadServerTLS(directory)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/nacos/ready" {
			t.Fatalf("upstream path=%q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	address := unusedAddress(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	metrics := &counters{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPProxy(ctx, address, upstream.URL, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverIdentity}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, metrics)
	}()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS(t, directory, nil)}, Timeout: 5 * time.Second}
	response, err := waitHTTP(client, "https://"+address+"/nacos/ready")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || metrics.httpRequests.Load() != 1 {
		t.Fatalf("status=%d requests=%d", response.StatusCode, metrics.httpRequests.Load())
	}
	withoutIdentity := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS(t, directory, []tls.Certificate{})}, Timeout: time.Second}
	if _, err := withoutIdentity.Get("https://" + address + "/nacos/ready"); err == nil {
		t.Fatal("HTTP mTLS boundary accepted a client without an identity")
	}
}

func TestSecureGRPCProxyTerminatesMTLSAndForwardsBytes(t *testing.T) {
	directory := generateTestMaterial(t)
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		connection, acceptErr := backend.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	serverIdentity, pool, err := loadServerTLS(directory)
	if err != nil {
		t.Fatal(err)
	}
	address := unusedAddress(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	metrics := &counters{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveGRPCProxy(ctx, address, backend.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverIdentity}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, NextProtos: []string{"h2"}}, metrics)
	}()
	var connection *tls.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err = tls.Dial("tcp", address, clientTLS(t, directory, nil))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if connection.ConnectionState().NegotiatedProtocol != "h2" {
		t.Fatalf("negotiated protocol=%q", connection.ConnectionState().NegotiatedProtocol)
	}
	if _, err := connection.Write([]byte("grpc-fixture")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("grpc-fixture"))
	if _, err := io.ReadFull(connection, buffer); err != nil || string(buffer) != "grpc-fixture" {
		t.Fatalf("forwarded=%q err=%v", buffer, err)
	}
	if metrics.grpcConnections.Load() != 1 {
		t.Fatalf("gRPC connections=%d", metrics.grpcConnections.Load())
	}
}

func generateTestMaterial(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "tls")
	if err := generateMaterial(directory, time.Now()); err != nil {
		t.Fatal(err)
	}
	return directory
}

func clientTLS(t *testing.T, directory string, certificates []tls.Certificate) *tls.Config {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(directory, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("append private CA")
	}
	if certificates == nil {
		identity, err := tls.LoadX509KeyPair(filepath.Join(directory, "client.pem"), filepath.Join(directory, "client-key.pem"))
		if err != nil {
			t.Fatal(err)
		}
		certificates = []tls.Certificate{identity}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "nacos.internal", Certificates: certificates, NextProtos: []string{"h2"}}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitHTTP(client *http.Client, endpoint string) (*http.Response, error) {
	deadline := time.Now().Add(5 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		var response *http.Response
		response, err = client.Get(endpoint)
		if err == nil {
			return response, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, err
}
