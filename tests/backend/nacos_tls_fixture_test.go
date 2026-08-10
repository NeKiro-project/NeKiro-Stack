//go:build e2e

package invokerecord_test

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
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type secureNacosMaterial struct {
	server tls.Certificate
	caPool *x509.CertPool
}

type secureNacosFixture struct {
	tlsAttempts  atomic.Int64
	mtlsAttempts atomic.Int64
}

func startSecureNacosFixture(t *testing.T, env *acceptanceEnv) {
	t.Helper()
	material := loadSecureNacosMaterial(t, env.tlsRoot)
	fixture := &secureNacosFixture{}
	env.secureNacos = fixture
	target, err := url.Parse(env.nacosURL)
	if err != nil {
		t.Fatal(err)
	}
	target.Path = ""
	target.RawPath = ""
	start := func(port string, requireClient bool) {
		listener, err := net.Listen("tcp", "0.0.0.0:"+port)
		if err != nil {
			t.Fatalf("listen secure Nacos fixture: %v", err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(writer, "Nacos fixture upstream unavailable", http.StatusBadGateway)
		}
		attempts := &fixture.tlsAttempts
		if requireClient {
			attempts = &fixture.mtlsAttempts
		}
		serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{material.server}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			attempts.Add(1)
			return nil, nil
		}}
		if requireClient {
			serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
			serverTLS.ClientCAs = material.caPool
		}
		server := &http.Server{Handler: proxy, TLSConfig: serverTLS}
		tlsListener := tls.NewListener(listener, serverTLS)
		go func() { _ = server.Serve(tlsListener) }()
		t.Cleanup(func() {
			_ = server.Close()
		})
	}
	start(env.tlsPort, false)
	start(env.mtlsPort, true)
}

func loadSecureNacosMaterial(t *testing.T, directory string) secureNacosMaterial {
	t.Helper()
	server, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatalf("load generated secure Nacos server identity: %v", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(directory, "ca.pem"))
	if err != nil {
		t.Fatalf("load generated secure Nacos CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("parse generated secure Nacos CA")
	}
	return secureNacosMaterial{server: server, caPool: pool}
}

func assertSecureRegistrationFailureMatrix(t *testing.T, env acceptanceEnv) {
	t.Helper()
	for name, test := range map[string]struct {
		overrides        []string
		expectTLSAttempt bool
	}{
		"wrong CA": {overrides: []string{
			"RUNTIME_B_NACOS_TLS_CA_FILE=/var/run/nekiro-nacos-tls/wrong-ca.pem",
			"RUNTIME_B_NACOS_TLS_CLIENT_CERT_FILE=/var/run/nekiro-nacos-tls/client.pem",
			"RUNTIME_B_NACOS_TLS_CLIENT_KEY_FILE=/var/run/nekiro-nacos-tls/client-key.pem",
		}, expectTLSAttempt: true},
		"wrong server name": {overrides: []string{
			"RUNTIME_B_NACOS_TLS_CA_FILE=/var/run/nekiro-nacos-tls/ca.pem",
			"RUNTIME_B_NACOS_TLS_SERVER_NAME=other.internal",
			"RUNTIME_B_NACOS_TLS_CLIENT_CERT_FILE=/var/run/nekiro-nacos-tls/client.pem",
			"RUNTIME_B_NACOS_TLS_CLIENT_KEY_FILE=/var/run/nekiro-nacos-tls/client-key.pem",
		}, expectTLSAttempt: true},
		"missing mTLS client": {overrides: []string{
			"RUNTIME_B_NACOS_TLS_CA_FILE=/var/run/nekiro-nacos-tls/ca.pem",
			"RUNTIME_B_NACOS_TLS_SERVER_NAME=nacos.internal",
			"RUNTIME_B_NACOS_TLS_CLIENT_CERT_FILE=",
			"RUNTIME_B_NACOS_TLS_CLIENT_KEY_FILE=",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			service := "runtime-b-negative-" + strings.ReplaceAll(strings.ToLower(name), " ", "-")
			args := []string{"--profile", "runtime-registration", "run", "--rm", "--no-deps"}
			for _, override := range test.overrides {
				args = append(args, "-e", override)
			}
			args = append(args,
				"-e", "RUNTIME_B_INSTANCE_ID="+service,
				"-e", "RUNTIME_B_NACOS_SERVICE_NAME="+service,
				"runtime-b-directory",
			)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			beforeAttempts := env.secureNacos.mtlsAttempts.Load()
			output, err := composeCommand(ctx, env, args...).CombinedOutput()
			cancel()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("invalid secure registration remained running instead of failing closed: %s", output)
			}
			if err == nil {
				t.Fatalf("invalid secure registration unexpectedly succeeded: %s", output)
			}
			if test.expectTLSAttempt && env.secureNacos.mtlsAttempts.Load() <= beforeAttempts {
				t.Fatalf("invalid secure registration failed before reaching the mTLS boundary: %s", output)
			}
			outputText := string(output)
			for _, forbidden := range []string{"/var/run/nekiro-nacos-tls", "PRIVATE KEY", "BEGIN CERTIFICATE", "wrong-ca.pem"} {
				if strings.Contains(outputText, forbidden) {
					t.Fatalf("secure registration failure leaked %q: %s", forbidden, outputText)
				}
			}
			assertNoNacosInstance(t, env, service)
		})
	}
}

func assertNoNacosInstance(t *testing.T, env acceptanceEnv, service string) {
	t.Helper()
	endpoint := env.nacosURL + "/v1/ns/instance/list?serviceName=" + url.QueryEscape(service) + "&groupName=NEKIRO&clusters=DEFAULT&namespaceId=nekiro&healthyOnly=false"
	result := doRequest(t, &http.Client{Timeout: 5 * time.Second}, endpoint, http.MethodGet, "", "", nil)
	if result.status == http.StatusOK {
		var response struct {
			Hosts []json.RawMessage `json:"hosts"`
		}
		if err := json.Unmarshal(result.body, &response); err != nil {
			t.Fatalf("decode failed-registration Nacos response: %v body=%s", err, result.body)
		}
		if len(response.Hosts) != 0 {
			t.Fatalf("failed registration left a Nacos instance: %s", result.body)
		}
	}
}

func writeSecureNacosMaterial(t *testing.T, directory string) secureNacosMaterial {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "NeKiro E2E Nacos CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, content []byte) string {
		path := filepath.Join(directory, name)
		// These are ephemeral acceptance credentials mounted read-only into
		// non-root fixture containers; repository and product storage never see them.
		if err := os.WriteFile(path, content, 0o444); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	issue := func(name string, serial int64, usage x509.ExtKeyUsage, dnsNames []string) (tls.Certificate, string, string) {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, DNSNames: dnsNames, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, err := x509.CreateCertificate(rand.Reader, template, caCertificate, public, caPrivate)
		if err != nil {
			t.Fatal(err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyDER, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			t.Fatal(err)
		}
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		certFile, keyFile := write(name+".pem", certPEM), write(name+"-key.pem", keyPEM)
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return certificate, certFile, keyFile
	}
	server, _, _ := issue("server", 2, x509.ExtKeyUsageServerAuth, []string{"nacos.internal"})
	_, _, _ = issue("client", 3, x509.ExtKeyUsageClientAuth, nil)
	otherPublic, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherTemplate := &x509.Certificate{SerialNumber: big.NewInt(4), Subject: pkix.Name{CommonName: "Wrong CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	otherDER, err := x509.CreateCertificate(rand.Reader, otherTemplate, otherTemplate, otherPublic, otherPrivate)
	if err != nil {
		t.Fatal(err)
	}
	write("wrong-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}))
	pool := x509.NewCertPool()
	pool.AddCert(caCertificate)
	return secureNacosMaterial{server: server, caPool: pool}
}
