package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const maxTLSFileBytes = 1 << 20

type counters struct {
	httpRequests    atomic.Int64
	grpcConnections atomic.Int64
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: nacos-secure-fixture <generate|serve>")
	}
	var err error
	switch os.Args[1] {
	case "generate":
		if len(os.Args) != 3 {
			log.Fatal("usage: nacos-secure-fixture generate <absolute-output-directory>")
		}
		err = generateMaterial(os.Args[2], time.Now())
	case "serve":
		err = serve()
	default:
		log.Fatal("unsupported nacos-secure-fixture command")
	}
	if err != nil {
		log.Fatal(err)
	}
}

func generateMaterial(directory string, now time.Time) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("output directory must be a clean absolute path")
	}
	// The directory is mounted read-only into non-root fixture containers. The
	// material is ephemeral and removed with the CI workspace.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return errors.New("create TLS output directory")
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("generate CA key")
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "NeKiro Stack Nacos E2E CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		return errors.New("create CA certificate")
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return errors.New("parse CA certificate")
	}
	if err := writeExclusive(filepath.Join(directory, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o444); err != nil {
		return err
	}
	if err := issueCertificate(directory, "server", 2, x509.ExtKeyUsageServerAuth, []string{"nacos.internal"}, caCertificate, caPrivate, now); err != nil {
		return err
	}
	if err := issueCertificate(directory, "client", 3, x509.ExtKeyUsageClientAuth, nil, caCertificate, caPrivate, now); err != nil {
		return err
	}
	otherPublic, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("generate negative CA key")
	}
	otherTemplate := &x509.Certificate{SerialNumber: big.NewInt(4), Subject: pkix.Name{CommonName: "Wrong CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	otherDER, err := x509.CreateCertificate(rand.Reader, otherTemplate, otherTemplate, otherPublic, otherPrivate)
	if err != nil {
		return errors.New("create negative CA certificate")
	}
	return writeExclusive(filepath.Join(directory, "wrong-ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}), 0o444)
}

func issueCertificate(directory, name string, serial int64, usage x509.ExtKeyUsage, dnsNames []string, ca *x509.Certificate, caPrivate ed25519.PrivateKey, now time.Time) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("generate leaf key")
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: dnsNames, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caPrivate)
	if err != nil {
		return errors.New("create leaf certificate")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return errors.New("marshal leaf key")
	}
	if err := writeExclusive(filepath.Join(directory, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o444); err != nil {
		return err
	}
	return writeExclusive(filepath.Join(directory, name+"-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o444)
}

func writeExclusive(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return errors.New("create TLS material")
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return errors.New("write TLS material")
	}
	if err := file.Close(); err != nil {
		return errors.New("close TLS material")
	}
	return nil
}

func serve() error {
	tlsRoot := os.Getenv("NEKIRO_NACOS_FIXTURE_TLS_ROOT")
	if !filepath.IsAbs(tlsRoot) || filepath.Clean(tlsRoot) != tlsRoot {
		return errors.New("TLS root must be a clean absolute path")
	}
	serverCertificate, caPool, err := loadServerTLS(tlsRoot)
	if err != nil {
		return err
	}
	sharedTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: caPool}
	grpcTLS := sharedTLS.Clone()
	grpcTLS.NextProtos = []string{"h2"}
	metrics := &counters{}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 3)
	go func() {
		errCh <- serveHTTPProxy(ctx, requiredEnv("NEKIRO_NACOS_FIXTURE_HTTP_LISTEN"), requiredEnv("NEKIRO_NACOS_FIXTURE_HTTP_UPSTREAM"), sharedTLS.Clone(), metrics)
	}()
	go func() {
		errCh <- serveGRPCProxy(ctx, requiredEnv("NEKIRO_NACOS_FIXTURE_GRPC_LISTEN"), requiredEnv("NEKIRO_NACOS_FIXTURE_GRPC_UPSTREAM"), grpcTLS, metrics)
	}()
	go func() { errCh <- serveStatus(ctx, requiredEnv("NEKIRO_NACOS_FIXTURE_STATUS_LISTEN"), metrics) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func loadServerTLS(root string) (tls.Certificate, *x509.CertPool, error) {
	certificate, err := tls.LoadX509KeyPair(filepath.Join(root, "server.pem"), filepath.Join(root, "server-key.pem"))
	if err != nil {
		return tls.Certificate{}, nil, errors.New("load fixture server identity")
	}
	caPEM, err := readLimited(filepath.Join(root, "ca.pem"))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return tls.Certificate{}, nil, errors.New("parse fixture private CA")
	}
	return certificate, pool, nil
}

func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open fixture TLS file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTLSFileBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxTLSFileBytes {
		return nil, errors.New("read fixture TLS file")
	}
	return data, nil
}

func serveHTTPProxy(ctx context.Context, listenAddress, upstream string, tlsConfig *tls.Config, metrics *counters) error {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme != "http" || target.Host == "" {
		return errors.New("invalid HTTP upstream")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "Nacos upstream unavailable", http.StatusBadGateway)
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		metrics.httpRequests.Add(1)
		proxy.ServeHTTP(writer, request)
	})
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return errors.New("listen on secure Nacos HTTP boundary")
	}
	server := &http.Server{Handler: handler}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveGRPCProxy(ctx context.Context, listenAddress, upstream string, tlsConfig *tls.Config, metrics *counters) error {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return errors.New("listen on secure Nacos gRPC boundary")
	}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("accept secure Nacos gRPC connection")
		}
		go proxyGRPC(connection, upstream, tlsConfig, metrics)
	}
}

func proxyGRPC(connection net.Conn, upstream string, tlsConfig *tls.Config, metrics *counters) {
	defer connection.Close()
	secure := tls.Server(connection, tlsConfig)
	if err := secure.Handshake(); err != nil {
		return
	}
	backend, err := net.DialTimeout("tcp", upstream, 5*time.Second)
	if err != nil {
		return
	}
	defer backend.Close()
	metrics.grpcConnections.Add(1)
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(backend, secure); _ = backend.(*net.TCPConn).CloseWrite(); done <- struct{}{} }()
	_, _ = io.Copy(secure, backend)
	<-done
}

func serveStatus(ctx context.Context, listenAddress string, metrics *counters) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/status", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]int64{"httpRequests": metrics.httpRequests.Load(), "grpcConnections": metrics.grpcConnections.Load()})
	})
	server := &http.Server{Addr: listenAddress, Handler: mux}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(fmt.Sprintf("%s must be set", name))
	}
	return value
}
