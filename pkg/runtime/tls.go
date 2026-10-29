package runtime

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/cnabio/cnab-go/secrets/host"
	"github.com/cnabio/cnab-go/valuesource"

	"github.com/sntns/nemo/pkg/target"
)

// tlsPEM is the PEM of the target's TLS, read from its sources: those not
// given are empty.
type tlsPEM struct{ ca, cert, key []byte }

func readTLS(given target.TLS) (tlsPEM, error) {
	var read tlsPEM
	for _, part := range []struct {
		name   string
		source *valuesource.Source
		into   *[]byte
	}{{"CA", given.CA, &read.ca}, {"certificate", given.Cert, &read.cert}, {"key", given.Key, &read.key}} {
		if part.source == nil {
			continue
		}
		value, err := (&host.SecretStore{}).Resolve(part.source.Key, part.source.Value)
		if err != nil {
			return read, fmt.Errorf("the target's TLS: reading its %s: %w", part.name, err)
		}
		*part.into = []byte(value)
	}
	return read, nil
}

// tlsClient is the HTTP client of a Docker whose TLS the target gives: its
// CA, else the system's, and the client's certificate.
func tlsClient(given target.TLS) (*http.Client, error) {
	read, err := readTLS(given)
	if err != nil {
		return nil, err
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if read.ca != nil {
		config.RootCAs = x509.NewCertPool()
		if !config.RootCAs.AppendCertsFromPEM(read.ca) {
			return nil, errors.New("the target's TLS: its CA is not a PEM certificate")
		}
	}
	if read.cert != nil || read.key != nil {
		certificate, err := tls.X509KeyPair(read.cert, read.key)
		if err != nil {
			return nil, errors.New("the target's TLS: its certificate and key are not a PEM pair: " + err.Error())
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: config}}, nil
}
