package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gocloud.dev/pubsub/kafkapubsub"
)

func clearKafkaEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		kafkaBrokersEnv,
		kafkaTLSEnableEnv,
		kafkaTLSCAEnv,
		kafkaTLSCertEnv,
		kafkaTLSKeyEnv,
		kafkaTLSSkipVerifyEnv,
	} {
		t.Setenv(name, "")
	}
}

func TestIsKafkaURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		topicURL string
		want     bool
	}{
		{name: "Kafka", topicURL: "kafka://events", want: true},
		{name: "Kafka with options", topicURL: "kafka://events?key_name=action", want: true},
		{name: "Other scheme", topicURL: "nats://events", want: false},
		{name: "Scheme prefix", topicURL: "kafka+tls://events", want: false},
		{name: "Malformed URL", topicURL: "://events", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isKafkaURL(tt.topicURL); got != tt.want {
				t.Fatalf("isKafkaURL(%q) = %v, want %v", tt.topicURL, got, tt.want)
			}
		})
	}
}

func TestParseEnvBool(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "Unset", value: "", want: false},
		{name: "True", value: "true", want: true},
		{name: "Uppercase true", value: "TRUE", want: true},
		{name: "One", value: "1", want: true},
		{name: "False", value: "false", want: false},
		{name: "Whitespace", value: "  true  ", want: true},
		{name: "Invalid", value: "enabled", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(kafkaTLSEnableEnv, tt.value)
			got, err := parseEnvBool(kafkaTLSEnableEnv)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseEnvBool() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseEnvBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKafkaBrokers(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr bool
	}{
		{name: "Single", value: "broker:9092", want: []string{"broker:9092"}},
		{name: "Trimmed list", value: " broker-a:9092, broker-b:9093 ", want: []string{"broker-a:9092", "broker-b:9093"}},
		{name: "Missing", wantErr: true},
		{name: "Whitespace", value: "  ", wantErr: true},
		{name: "Leading comma", value: ",broker:9092", wantErr: true},
		{name: "Trailing comma", value: "broker:9092,", wantErr: true},
		{name: "Empty middle entry", value: "broker-a:9092,,broker-b:9092", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(kafkaBrokersEnv, tt.value)
			got, err := kafkaBrokers()
			if (err != nil) != tt.wantErr {
				t.Fatalf("kafkaBrokers() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("kafkaBrokers() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCreateKafkaTLSConfig(t *testing.T) {
	t.Run("Secure defaults", func(t *testing.T) {
		clearKafkaEnv(t)
		config, err := createKafkaTLSConfig()
		if err != nil {
			t.Fatalf("createKafkaTLSConfig() error = %v", err)
		}
		if config.InsecureSkipVerify {
			t.Fatal("server certificate verification must be enabled by default")
		}
		if config.MinVersion != tls.VersionTLS12 {
			t.Fatalf("MinVersion = %d, want TLS 1.2", config.MinVersion)
		}
	})

	t.Run("Invalid skip verify", func(t *testing.T) {
		clearKafkaEnv(t)
		t.Setenv(kafkaTLSSkipVerifyEnv, "sometimes")
		if _, err := createKafkaTLSConfig(); err == nil {
			t.Fatal("createKafkaTLSConfig() succeeded with an invalid boolean")
		}
	})

	for _, tt := range []struct {
		name     string
		certFile string
		keyFile  string
	}{
		{name: "Certificate only", certFile: "client.crt"},
		{name: "Key only", keyFile: "client.key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearKafkaEnv(t)
			t.Setenv(kafkaTLSCertEnv, tt.certFile)
			t.Setenv(kafkaTLSKeyEnv, tt.keyFile)
			if _, err := createKafkaTLSConfig(); err == nil {
				t.Fatal("createKafkaTLSConfig() succeeded with an incomplete client certificate pair")
			}
		})
	}

	t.Run("Invalid CA", func(t *testing.T) {
		clearKafkaEnv(t)
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(kafkaTLSCAEnv, caFile)
		if _, err := createKafkaTLSConfig(); err == nil {
			t.Fatal("createKafkaTLSConfig() succeeded with an invalid CA")
		}
	})

	t.Run("Valid CA and client certificate", func(t *testing.T) {
		clearKafkaEnv(t)
		certFile, keyFile := writeTestCertificate(t)
		t.Setenv(kafkaTLSCAEnv, certFile)
		t.Setenv(kafkaTLSCertEnv, certFile)
		t.Setenv(kafkaTLSKeyEnv, keyFile)

		config, err := createKafkaTLSConfig()
		if err != nil {
			t.Fatalf("createKafkaTLSConfig() error = %v", err)
		}
		if config.RootCAs == nil {
			t.Fatal("RootCAs is nil")
		}
		if len(config.Certificates) != 1 {
			t.Fatalf("Certificates length = %d, want 1", len(config.Certificates))
		}
	})
}

func TestKafkaURLOpener(t *testing.T) {
	t.Run("Default configuration", func(t *testing.T) {
		clearKafkaEnv(t)
		t.Setenv(kafkaBrokersEnv, "broker:9092")

		opener, tlsEnabled, err := kafkaURLOpener()
		if err != nil {
			t.Fatalf("kafkaURLOpener() error = %v", err)
		}
		if tlsEnabled || opener.Config.Net.TLS.Enable {
			t.Fatal("TLS enabled without explicit configuration")
		}
		if opener.Config.Version != kafkapubsub.MinimalConfig().Version {
			t.Fatalf("Kafka version = %v, want upstream minimal version %v", opener.Config.Version, kafkapubsub.MinimalConfig().Version)
		}
		if err := opener.Config.Validate(); err != nil {
			t.Fatalf("Sarama configuration is invalid: %v", err)
		}
	})

	t.Run("TLS enabled", func(t *testing.T) {
		clearKafkaEnv(t)
		t.Setenv(kafkaBrokersEnv, "broker:9092")
		t.Setenv(kafkaTLSEnableEnv, "true")

		opener, tlsEnabled, err := kafkaURLOpener()
		if err != nil {
			t.Fatalf("kafkaURLOpener() error = %v", err)
		}
		if !tlsEnabled || !opener.Config.Net.TLS.Enable || opener.Config.Net.TLS.Config == nil {
			t.Fatal("TLS configuration was not applied to Sarama")
		}
	})
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kafka-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestKafkaTLSErrorsDoNotExposeFileContents(t *testing.T) {
	clearKafkaEnv(t)
	secret := "private-key-material"
	keyFile := filepath.Join(t.TempDir(), "client.key")
	if err := os.WriteFile(keyFile, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(kafkaTLSCertEnv, keyFile)
	t.Setenv(kafkaTLSKeyEnv, keyFile)

	_, err := createKafkaTLSConfig()
	if err == nil {
		t.Fatal("createKafkaTLSConfig() succeeded with invalid credentials")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("TLS error exposed credential contents")
	}
}
