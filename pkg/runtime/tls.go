package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"

	"github.com/sntns/nemo/pkg/target"
)

// tlsClient is the HTTP client of a Docker whose TLS is held in memory: its
// CA, else the system's, and the client's certificate.
func tlsClient(given target.TLS) (*http.Client, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if given.CA != "" {
		config.RootCAs = x509.NewCertPool()
		if !config.RootCAs.AppendCertsFromPEM([]byte(given.CA)) {
			return nil, errors.New("the target's TLS: its CA is not a PEM certificate")
		}
	}
	if given.Cert != "" || given.Key != "" {
		certificate, err := tls.X509KeyPair([]byte(given.Cert), []byte(given.Key))
		if err != nil {
			return nil, errors.New("the target's TLS: its certificate and key are not a PEM pair: " + err.Error())
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: config}}, nil
}
