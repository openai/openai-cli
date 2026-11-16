// Package mtls provides synthetic certificate fixtures for mTLS tests.
package mtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PKI contains synthetic PEM data for mTLS tests.
type PKI struct {
	RootPEM        []byte
	ClientLeafPEM  []byte
	ClientChainPEM []byte
	ClientKeyPEM   []byte
}

// NewPKI creates a synthetic root, intermediate, and client certificate chain.
func NewPKI(t *testing.T) PKI {
	t.Helper()

	now := time.Now()
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mTLS test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, rootCertificate, rootKey := createTestCertificate(t, rootTemplate, rootTemplate, nil)

	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "mTLS test intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	intermediateDER, intermediateCertificate, intermediateKey := createTestCertificate(
		t,
		intermediateTemplate,
		rootCertificate,
		rootKey,
	)

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mTLS test client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _, clientKey := createTestCertificate(
		t,
		clientTemplate,
		intermediateCertificate,
		intermediateKey,
	)

	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	require.NoError(t, err)
	clientLeafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	intermediatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediateDER})

	return PKI{
		RootPEM:        pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		ClientLeafPEM:  clientLeafPEM,
		ClientChainPEM: append(clientLeafPEM, intermediatePEM...),
		ClientKeyPEM:   pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER}),
	}
}

func createTestCertificate(
	t *testing.T,
	template *x509.Certificate,
	parent *x509.Certificate,
	parentKey *ecdsa.PrivateKey,
) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	if parentKey == nil {
		parentKey = key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return der, certificate, key
}

// WriteClientFiles writes synthetic client credentials to private temporary files.
func WriteClientFiles(t *testing.T, certPEM, keyPEM []byte) (string, string) {
	t.Helper()

	dir := t.TempDir()
	certFile := filepath.Join(dir, "client-chain.pem")
	keyFile := filepath.Join(dir, "client-key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	return certFile, keyFile
}
