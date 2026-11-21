// Copyright 2026 The Tardigrade Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
)

// testCA is a self-signed CA used to issue test leaf certs, keeping
// each test's PKI self-contained rather than relying on real files.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return &testCA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// issue signs a new leaf cert/key pair for cn, valid for serving
// (ExtKeyUsageServerAuth) and client authentication
// (ExtKeyUsageClientAuth), and writes both as PEM files under dir.
func (ca *testCA) issue(t *testing.T, dir, cn string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certFile = filepath.Join(dir, cn+"-cert.pem")
	keyFile = filepath.Join(dir, cn+"-key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	return certFile, keyFile
}

func TestServerTLSCredentialsRequiresAllFields(t *testing.T) {
	_, err := serverTLSCredentials(cfg.TLS{})
	assert.Error(t, err)
}

func TestServerTLSCredentialsBadClientCA(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	certFile, keyFile := ca.issue(t, dir, "server")

	badCA := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(badCA, []byte("not a pem cert"), 0o600))

	_, err := serverTLSCredentials(cfg.TLS{CertFile: certFile, KeyFile: keyFile, ClientCAFile: badCA})
	assert.Error(t, err)
}

// TestMTLSHandshake builds real server credentials via
// serverTLSCredentials and drives a real gRPC connection through
// them, proving the behavior serverTLSCredentials exists for: a
// client cert signed by the trusted CA is accepted; no client cert
// at all is rejected during the TLS handshake.
func TestMTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	caFile := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile, ca.certPEM, 0o600))

	serverCertFile, serverKeyFile := ca.issue(t, dir, "server")
	clientCertFile, clientKeyFile := ca.issue(t, dir, "client")

	creds, err := serverTLSCredentials(cfg.TLS{
		CertFile:     serverCertFile,
		KeyFile:      serverKeyFile,
		ClientCAFile: caFile,
	})
	require.NoError(t, err)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()

	s := grpc.NewServer(grpc.Creds(creds))
	v1.RegisterRuntimeServiceServer(s, &stubRuntimeServiceServer{})
	go s.Serve(lis)
	defer s.Stop()

	t.Run("authorized client succeeds", func(t *testing.T) {
		clientCert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
		require.NoError(t, err)
		caPool := x509.NewCertPool()
		require.True(t, caPool.AppendCertsFromPEM(ca.certPEM))

		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
				ServerName:   "127.0.0.1",
			}),
		))
		require.NoError(t, err)
		defer conn.Close()

		client := v1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = client.StartSandbox(ctx, &v1.StartSandboxRequest{SandboxId: "x"})
		require.NoError(t, err, "expected the RPC to actually reach the server, not fail the TLS handshake")
	})

	t.Run("no client cert is rejected", func(t *testing.T) {
		caPool := x509.NewCertPool()
		require.True(t, caPool.AppendCertsFromPEM(ca.certPEM))

		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{
				RootCAs:    caPool,
				ServerName: "127.0.0.1",
			}),
		))
		require.NoError(t, err)
		defer conn.Close()

		client := v1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = client.StartSandbox(ctx, &v1.StartSandboxRequest{SandboxId: "x"})
		assert.Error(t, err, "expected a client with no certificate to be rejected")
	})

	t.Run("client cert from an untrusted ca is rejected", func(t *testing.T) {
		otherCA := newTestCA(t)
		otherCertFile, otherKeyFile := otherCA.issue(t, dir, "other-client")
		clientCert, err := tls.LoadX509KeyPair(otherCertFile, otherKeyFile)
		require.NoError(t, err)
		caPool := x509.NewCertPool()
		require.True(t, caPool.AppendCertsFromPEM(ca.certPEM))

		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
				ServerName:   "127.0.0.1",
			}),
		))
		require.NoError(t, err)
		defer conn.Close()

		client := v1.NewRuntimeServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = client.StartSandbox(ctx, &v1.StartSandboxRequest{SandboxId: "x"})
		assert.Error(t, err, "expected a client cert from an untrusted CA to be rejected")
	})
}

type stubRuntimeServiceServer struct {
	v1.UnimplementedRuntimeServiceServer
}

func (stubRuntimeServiceServer) StartSandbox(context.Context, *v1.StartSandboxRequest) (*v1.StartSandboxResponse, error) {
	return &v1.StartSandboxResponse{}, nil
}
