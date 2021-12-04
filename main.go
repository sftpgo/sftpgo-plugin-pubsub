package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"gocloud.dev/pubsub"
	_ "gocloud.dev/pubsub/awssnssqs"
	_ "gocloud.dev/pubsub/azuresb"
	_ "gocloud.dev/pubsub/gcppubsub"
	_ "gocloud.dev/pubsub/kafkapubsub"
	_ "gocloud.dev/pubsub/natspubsub"
	_ "gocloud.dev/pubsub/rabbitpubsub"

	"github.com/drakkan/sftpgo/v2/sdk/plugin/notifier"
)

const version = "1.0.0-dev"

var (
	commitHash = ""
	date       = ""
)

var appLogger = hclog.New(&hclog.LoggerOptions{
	DisableTime: true,
	Level:       hclog.Debug,
})

type fsEvent struct {
	Timestamp         string `json:"timestamp"`
	Action            string `json:"action"`
	Username          string `json:"username"`
	FsPath            string `json:"fs_path"`
	FsTargetPath      string `json:"fs_target_path,omitempty"`
	VirtualPath       string `json:"virtual_path"`
	VirtualTargetPath string `json:"virtual_target_path,omitempty"`
	SSHCmd            string `json:"ssh_cmd,omitempty"`
	FileSize          int64  `json:"file_size,omitempty"`
	Status            int    `json:"status"`
	Protocol          string `json:"protocol"`
	IP                string `json:"ip"`
	SessionID         string `json:"session_id"`
	InstanceID        string `json:"instance_id,omitempty"`
}

type providerEvent struct {
	Timestamp  string `json:"timestamp"`
	Action     string `json:"action"`
	Username   string `json:"username"`
	IP         string `json:"ip"`
	ObjectType string `json:"object_type"`
	ObjectName string `json:"object_name"`
	ObjectData []byte `json:"object_data"`
	InstanceID string `json:"instance_id,omitempty"`
}

type pubSubNotifier struct {
	topic      *pubsub.Topic
	timeout    time.Duration
	instanceID string
}

func (n *pubSubNotifier) NotifyFsEvent(timestamp int64, action, username, fsPath, fsTargetPath, sshCmd, protocol, ip,
	virtualPath, virtualTargetPath, sessionID string, fileSize int64, status int,
) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := fsEvent{
		Timestamp:         getTimeFromNsecSinceEpoch(timestamp).UTC().Format(time.RFC3339Nano),
		Action:            action,
		Username:          username,
		FsPath:            fsPath,
		FsTargetPath:      fsTargetPath,
		VirtualPath:       virtualPath,
		VirtualTargetPath: virtualTargetPath,
		Protocol:          protocol,
		IP:                ip,
		SessionID:         sessionID,
		FileSize:          fileSize,
		Status:            status,
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
			"action": action,
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish fs event to topic", "action", action, "username", username,
			"virtual path", virtualPath, "error", err)
		panic(err)
	}
	return nil
}

func (n *pubSubNotifier) NotifyProviderEvent(timestamp int64, action, username, objectType, objectName, ip string,
	object []byte,
) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := providerEvent{
		Timestamp:  getTimeFromNsecSinceEpoch(timestamp).UTC().Format(time.RFC3339Nano),
		Action:     action,
		Username:   username,
		IP:         ip,
		ObjectType: objectType,
		ObjectName: objectName,
		ObjectData: object,
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
			"action":      action,
			"object_type": objectType,
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish provider event to topic", "action", action, "error", err)
		panic(err)
	}
	return nil
}

func getTimeFromNsecSinceEpoch(nsec int64) time.Time {
	return time.Unix(0, nsec)
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

	topic, err := pubsub.OpenTopic(ctx, topicUrl)
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
