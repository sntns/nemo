package runtime

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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sntns/nemo/pkg/target"
)

// authority signs certificates for the tests, in memory.
type authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         string
}

func newAuthority(t *testing.T) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return authority{certificate: certificate, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// sign returns a certificate and its key, in PEM, for a server or a client.
func (ca authority) sign(t *testing.T, usage x509.ExtKeyUsage) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// A Docker that checks its clients is reached with the TLS the target holds
// in memory, no file nor variable written.
func TestConnect_tls(t *testing.T) {
	t.Parallel()
	ca := newAuthority(t)
	serverCert, serverKey := ca.sign(t, x509.ExtKeyUsageServerAuth)
	pair, err := tls.X509KeyPair([]byte(serverCert), []byte(serverKey))
	require.NoError(t, err)
	daemon := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/_ping") {
			response.Header().Set("Api-Version", "1.52")
			_, _ = response.Write([]byte("OK"))
		}
	}))
	clients := x509.NewCertPool()
	clients.AddCert(ca.certificate)
	daemon.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clients}
	daemon.StartTLS()
	defer daemon.Close()
	host := "tcp://" + daemon.Listener.Addr().String()

	clientCert, clientKey := ca.sign(t, x509.ExtKeyUsageClientAuth)
	api, err := Connect(target.Target{Configuration: target.Configuration{Host: host, TLS: &target.TLS{CA: ca.pem, Cert: clientCert, Key: clientKey}}})
	require.NoError(t, err)
	defer api.Close()
	_, err = api.Ping(t.Context(), client.PingOptions{})
	assert.NoError(t, err, "the client's certificate, checked by the daemon")

	anonymous, err := Connect(target.Target{Configuration: target.Configuration{Host: host, TLS: &target.TLS{CA: ca.pem}}})
	require.NoError(t, err)
	defer anonymous.Close()
	_, err = anonymous.Ping(t.Context(), client.PingOptions{})
	assert.Error(t, err, "no client certificate")

	_, err = Connect(target.Target{Configuration: target.Configuration{Host: host, TLS: &target.TLS{CA: "not a certificate"}}})
	assert.ErrorContains(t, err, "its CA is not a PEM certificate")
}
