package credentialbundle

import (
	"bytes"
	"strings"
	"testing"
)

func TestSealOpenRoundTripAndRejectsTampering(t *testing.T) {
	certificate := []byte("-----BEGIN CERTIFICATE-----\ncertificate-marker\n-----END CERTIFICATE-----")
	privateKey := []byte("-----BEGIN PRIVATE KEY-----\nprivate-key-marker\n-----END PRIVATE KEY-----")
	ca := []byte("-----BEGIN CERTIFICATE-----\nca-marker\n-----END CERTIFICATE-----")
	keyHex, bundleHex, err := Seal(certificate, privateKey, ca)
	if err != nil {
		t.Fatalf("seal credentials: %v", err)
	}
	for _, marker := range []string{"certificate-marker", "private-key-marker", "ca-marker"} {
		if strings.Contains(bundleHex, marker) {
			t.Fatalf("encrypted bundle exposes %q", marker)
		}
	}
	gotCertificate, gotPrivateKey, gotCA, err := Open(keyHex, bundleHex)
	if err != nil {
		t.Fatalf("open credentials: %v", err)
	}
	if !bytes.Equal(gotCertificate, certificate) || !bytes.Equal(gotPrivateKey, privateKey) || !bytes.Equal(gotCA, ca) {
		t.Fatal("decrypted credentials differ from inputs")
	}
	replacement := "0"
	if strings.HasSuffix(bundleHex, "0") {
		replacement = "1"
	}
	tampered := bundleHex[:len(bundleHex)-1] + replacement
	if _, _, _, err := Open(keyHex, tampered); err == nil {
		t.Fatal("expected tampered bundle to be rejected")
	}
}
