// Copyright (C) 2021 Nicola Murino
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, version 3.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"gocloud.dev/pubsub"
	_ "gocloud.dev/pubsub/awssnssqs"
	_ "gocloud.dev/pubsub/azuresb"
	_ "gocloud.dev/pubsub/gcppubsub"
	"gocloud.dev/pubsub/kafkapubsub"
	_ "gocloud.dev/pubsub/natspubsub"
	_ "gocloud.dev/pubsub/rabbitpubsub"

	"github.com/sftpgo/sdk/plugin/notifier"
)

const version = "1.0.16-tls"

var (
	commitHash = ""
	date       = ""
)

var appLogger = hclog.New(&hclog.LoggerOptions{
	DisableTime: true,
	Level:       hclog.Debug,
})

type fsEvent struct {
	Timestamp         string            `json:"timestamp"`
	Action            string            `json:"action"`
	Username          string            `json:"username"`
	FsPath            string            `json:"fs_path"`
	FsTargetPath      string            `json:"fs_target_path,omitempty"`
	VirtualPath       string            `json:"virtual_path"`
	VirtualTargetPath string            `json:"virtual_target_path,omitempty"`
	SSHCmd            string            `json:"ssh_cmd,omitempty"`
	FileSize          int64             `json:"file_size,omitempty"`
	Elapsed           int64             `json:"elapsed,omitempty"`
	Status            int               `json:"status"`
	Protocol          string            `json:"protocol"`
	IP                string            `json:"ip"`
	SessionID         string            `json:"session_id"`
	FsProvider        int               `json:"fs_provider"`
	Bucket            string            `json:"bucket,omitempty"`
	Endpoint          string            `json:"endpoint,omitempty"`
	OpenFlags         int               `json:"open_flags,omitempty"`
	Role              string            `json:"role,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	InstanceID        string            `json:"instance_id,omitempty"`
}

type providerEvent struct {
	Timestamp  string `json:"timestamp"`
	Action     string `json:"action"`
	Username   string `json:"username"`
	IP         string `json:"ip"`
	ObjectType string `json:"object_type"`
	ObjectName string `json:"object_name"`
	ObjectData []byte `json:"object_data"`
	Role       string `json:"role,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

type LogEvent struct {
	Timestamp  string `json:"timestamp"`
	Event      int    `json:"event"`
	Protocol   string `json:"protocol,omitempty"`
	Username   string `json:"username,omitempty"`
	IP         string `json:"ip,omitempty"`
	Message    string `json:"message,omitempty"`
	Role       string `json:"role,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
}

type pubSubNotifier struct {
	topic      *pubsub.Topic
	timeout    time.Duration
	instanceID string
}

func (n *pubSubNotifier) NotifyFsEvent(event *notifier.FsEvent) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := fsEvent{
		Timestamp:         getTimeFromNsecSinceEpoch(event.Timestamp).UTC().Format(time.RFC3339Nano),
		Action:            event.Action,
		Username:          event.Username,
		FsPath:            event.Path,
		FsTargetPath:      event.TargetPath,
		VirtualPath:       event.VirtualPath,
		VirtualTargetPath: event.VirtualTargetPath,
		Protocol:          event.Protocol,
		IP:                event.IP,
		SessionID:         event.SessionID,
		FileSize:          event.FileSize,
		Elapsed:           event.Elapsed,
		Status:            event.Status,
		FsProvider:        event.FsProvider,
		Bucket:            event.Bucket,
		Endpoint:          event.Endpoint,
		OpenFlags:         event.OpenFlags,
		Role:              event.Role,
		Metadata:          event.Metadata,
		InstanceID:        n.instanceID,
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		appLogger.Warn("unable to marshal fs event", "error", err)
		return err
	}

	err = n.topic.Send(ctx, &pubsub.Message{
		Body: msg,
		Metadata: map[string]string{
			"action": event.Action,
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish fs event to topic", "action", event.Action, "username",
			event.Username, "virtual path", event.VirtualPath, "error", err)
		panic(err)
	}
	return nil
}

func (n *pubSubNotifier) NotifyProviderEvent(event *notifier.ProviderEvent) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := providerEvent{
		Timestamp:  getTimeFromNsecSinceEpoch(event.Timestamp).UTC().Format(time.RFC3339Nano),
		Action:     event.Action,
		Username:   event.Username,
		IP:         event.IP,
		ObjectType: event.ObjectType,
		ObjectName: event.ObjectName,
		ObjectData: event.ObjectData,
		Role:       event.Role,
		InstanceID: n.instanceID,
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		appLogger.Warn("unable to marshal provider event", "error", err)
		return err
	}

	err = n.topic.Send(ctx, &pubsub.Message{
		Body: msg,
		Metadata: map[string]string{
			"action":      event.Action,
			"object_type": event.ObjectType,
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish provider event to topic", "action", event.Action, "error", err)
		panic(err)
	}
	return nil
}

func (n *pubSubNotifier) NotifyLogEvent(event *notifier.LogEvent) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := LogEvent{
		Timestamp:  getTimeFromNsecSinceEpoch(event.Timestamp).UTC().Format(time.RFC3339Nano),
		Event:      int(event.Event),
		Protocol:   event.Protocol,
		Username:   event.Username,
		IP:         event.IP,
		Message:    event.Message,
		Role:       event.Role,
		InstanceID: n.instanceID,
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		appLogger.Warn("unable to marshal log event", "error", err)
		return err
	}

	err = n.topic.Send(ctx, &pubsub.Message{
		Body: msg,
		Metadata: map[string]string{
			"action": "log",
			"event":  strconv.Itoa(int(event.Event)),
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish log event to topic", "event", getLogEventString(event.Event), "error", err)
		panic(err)
	}
	return nil
}

func getTimeFromNsecSinceEpoch(nsec int64) time.Time {
	return time.Unix(0, nsec)
}

func getLogEventString(event notifier.LogEventType) string {
	switch event {
	case notifier.LogEventTypeLoginFailed:
		return "Login failed"
	case notifier.LogEventTypeLoginNoUser:
		return "Login with non-existent user"
	case notifier.LogEventTypeNoLoginTried:
		return "No login tried"
	case notifier.LogEventTypeNotNegotiated:
		return "Algorithm negotiation failed"
	default:
		return fmt.Sprintf("unknown type: %d", event)
	}
}

func getVersionString() string {
	var sb strings.Builder
	sb.WriteString(version)
	if commitHash != "" {
		sb.WriteString("-")
		sb.WriteString(commitHash)
	}
	if date != "" {
		sb.WriteString("-")
		sb.WriteString(date)
	}
	return sb.String()
}

// isKafkaURL checks if the URL scheme is "kafka"
func isKafkaURL(topicURL string) bool {
	return strings.HasPrefix(topicURL, "kafka://")
}

// getKafkaTopicName extracts the topic name from a kafka:// URL
func getKafkaTopicName(topicURL string) (string, error) {
	u, err := url.Parse(topicURL)
	if err != nil {
		return "", fmt.Errorf("invalid kafka URL: %w", err)
	}
	// Topic name is the host + path
	topicName := u.Host
	if u.Path != "" && u.Path != "/" {
		topicName = topicName + u.Path
	}
	if topicName == "" {
		return "", fmt.Errorf("topic name is required in kafka URL")
	}
	return topicName, nil
}

// createKafkaTLSConfig creates a TLS configuration from environment variables
// Environment variables:
//   - KAFKA_TLS_CA: path to CA certificate file
//   - KAFKA_TLS_CERT: path to client certificate file
//   - KAFKA_TLS_KEY: path to client key file
//   - KAFKA_TLS_SKIP_VERIFY: set to "true" to skip server certificate verification
func createKafkaTLSConfig() (*tls.Config, error) {
	caFile := os.Getenv("KAFKA_TLS_CA")
	certFile := os.Getenv("KAFKA_TLS_CERT")
	keyFile := os.Getenv("KAFKA_TLS_KEY")
	skipVerify := os.Getenv("KAFKA_TLS_SKIP_VERIFY") == "true"

	tlsConfig := &tls.Config{
		InsecureSkipVerify: skipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	// Load CA certificate if provided
	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", caFile, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsConfig.RootCAs = caCertPool
		appLogger.Info("loaded CA certificate", "file", caFile)
	}

	// Load client certificate and key if provided (for mTLS)
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate/key from %s/%s: %w", certFile, keyFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		appLogger.Info("loaded client certificate for mTLS", "cert", certFile, "key", keyFile)
	}

	return tlsConfig, nil
}

// openKafkaTopicWithTLS opens a Kafka topic with TLS configuration from environment variables
// Environment variables:
//   - KAFKA_BROKERS: comma-separated list of broker addresses (required)
//   - KAFKA_TLS_ENABLE: set to "true" to enable TLS
//   - KAFKA_TLS_CA, KAFKA_TLS_CERT, KAFKA_TLS_KEY: TLS certificate paths
func openKafkaTopicWithTLS(ctx context.Context, topicURL string) (*pubsub.Topic, error) {
	topicName, err := getKafkaTopicName(topicURL)
	if err != nil {
		return nil, err
	}

	brokerList := os.Getenv("KAFKA_BROKERS")
	if brokerList == "" {
		return nil, fmt.Errorf("KAFKA_BROKERS environment variable is required for Kafka")
	}
	brokers := strings.Split(brokerList, ",")
	for i, b := range brokers {
		brokers[i] = strings.TrimSpace(b)
	}

	appLogger.Info("configuring Kafka connection", "brokers", brokers, "topic", topicName)

	// Create sarama config
	config := sarama.NewConfig()
	config.Version = sarama.V2_0_0_0
	config.Producer.Return.Successes = true
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 3

	// Configure TLS if enabled
	tlsEnable := os.Getenv("KAFKA_TLS_ENABLE") == "true"
	if tlsEnable {
		appLogger.Info("TLS is enabled for Kafka connection")
		tlsConfig, err := createKafkaTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %w", err)
		}
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = tlsConfig
	}

	// Open topic using kafkapubsub directly (not via URL opener)
	topic, err := kafkapubsub.OpenTopic(brokers, config, topicName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open Kafka topic %s: %w", topicName, err)
	}

	appLogger.Info("successfully connected to Kafka topic", "topic", topicName, "tls", tlsEnable)
	return topic, nil
}

func main() {
	if len(os.Args) < 2 {
		appLogger.Error("please specify the topic url as command line argument")
		os.Exit(1)
	}
	var instanceID string
	topicUrl := os.Args[1]
	if len(os.Args) > 2 {
		instanceID = os.Args[2]
	}
	appLogger.Info("starting sftpgo-plugin-pubsub", "version", getVersionString(), "topic", topicUrl,
		"instance id", instanceID)

	ctx, cancelFn := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFn()

	var topic *pubsub.Topic
	var err error

	// Use custom Kafka opener with TLS support for kafka:// URLs
	if isKafkaURL(topicUrl) {
		topic, err = openKafkaTopicWithTLS(ctx, topicUrl)
	} else {
		// Use default Go CDK URL opener for other pubsub systems
		topic, err = pubsub.OpenTopic(ctx, topicUrl)
	}
	if err != nil {
		appLogger.Error("unable to open topic", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := topic.Shutdown(shutdownCtx)
		if err != nil {
			appLogger.Error("unable to close topic", "error", err)
		}
		cancel()
	}()

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: notifier.Handshake,
		Plugins: map[string]plugin.Plugin{
			notifier.PluginName: &notifier.Plugin{Impl: &pubSubNotifier{
				topic:      topic,
				timeout:    30 * time.Second,
				instanceID: instanceID,
			}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
