package store

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestTLSConfigHandshake(t *testing.T) {
	t.Parallel()
	ca, caKey, caPEM := makeTestCA(t)
	serverCertPEM, serverKeyPEM := makeTestCertificate(t, ca, caKey, "server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	clientCertPEM, clientKeyPEM := makeTestCertificate(t, ca, caKey, "client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	serverCertificate, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AppendCertsFromPEM(caPEM)

	t.Run("custom CA", func(t *testing.T) {
		clientConfig, err := makeTLSConfig("127.0.0.1", TLSRequest{Mode: "custom", CAPEM: string(caPEM)})
		if err != nil {
			t.Fatal(err)
		}
		serverConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}}
		if clientErr, serverErr := tlsHandshake(clientConfig, serverConfig); clientErr != nil || serverErr != nil {
			t.Fatalf("custom CA handshake: client=%v server=%v", clientErr, serverErr)
		}
	})

	t.Run("mutual TLS", func(t *testing.T) {
		clientConfig, err := makeTLSConfig("127.0.0.1", TLSRequest{
			Mode: "mutual", CAPEM: string(caPEM),
			ClientCertPEM: string(clientCertPEM), ClientKeyPEM: string(clientKeyPEM),
		})
		if err != nil {
			t.Fatal(err)
		}
		serverConfig := &tls.Config{
			MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate},
			ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots,
		}
		if clientErr, serverErr := tlsHandshake(clientConfig, serverConfig); clientErr != nil || serverErr != nil {
			t.Fatalf("mutual TLS handshake: client=%v server=%v", clientErr, serverErr)
		}
	})

	t.Run("rejects TLS 1.1", func(t *testing.T) {
		clientConfig, err := makeTLSConfig("127.0.0.1", TLSRequest{Mode: "custom", CAPEM: string(caPEM)})
		if err != nil {
			t.Fatal(err)
		}
		serverConfig := &tls.Config{MaxVersion: tls.VersionTLS11, Certificates: []tls.Certificate{serverCertificate}}
		clientErr, _ := tlsHandshake(clientConfig, serverConfig)
		if clientErr == nil {
			t.Fatal("TLS handshake accepted a protocol older than TLS 1.2")
		}
	})

	t.Run("wrong server name", func(t *testing.T) {
		clientConfig, err := makeTLSConfig("127.0.0.1", TLSRequest{Mode: "custom", ServerName: "wrong.example", CAPEM: string(caPEM)})
		if err != nil {
			t.Fatal(err)
		}
		serverConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCertificate}}
		clientErr, _ := tlsHandshake(clientConfig, serverConfig)
		if clientErr == nil {
			t.Fatal("TLS handshake accepted the wrong server name")
		}
	})
}

func tlsHandshake(clientConfig, serverConfig *tls.Config) (clientErr, serverErr error) {
	serverConn, clientConn := net.Pipe()
	deadline := time.Now().Add(3 * time.Second)
	_ = serverConn.SetDeadline(deadline)
	_ = clientConn.SetDeadline(deadline)
	serverTLS := tls.Server(serverConn, serverConfig)
	clientTLS := tls.Client(clientConn, clientConfig)
	done := make(chan error, 1)
	go func() {
		done <- serverTLS.Handshake()
		_ = serverTLS.Close()
	}()
	clientErr = clientTLS.Handshake()
	_ = clientTLS.Close()
	serverErr = <-done
	return clientErr, serverErr
}

func makeTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Rowlight test CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func makeTestCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string, usages []x509.ExtKeyUsage) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages,
	}
	if commonName == "server" {
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
