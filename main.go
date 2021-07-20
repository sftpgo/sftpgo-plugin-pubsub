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

const version = "0.9.0-dev"

var (
	commitHash = ""
	date       = ""
)

var appLogger = hclog.New(&hclog.LoggerOptions{
	DisableTime: true,
	Level:       hclog.Debug,
})

type fsEvent struct {
	Timestamp    string `json:"timestamp"`
	Action       string `json:"action"`
	Username     string `json:"username"`
	FsPath       string `json:"fs_path"`
	FsTargetPath string `json:"fs_target_path,omitempty"`
	SSHCmd       string `json:"ssh_cmd,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	Status       int    `json:"status"`
	Protocol     string `json:"protocol"`
}

type pubSubNotifier struct {
	topic   *pubsub.Topic
	timeout time.Duration
}

func (n *pubSubNotifier) NotifyFsEvent(timestamp time.Time, action, username, fsPath, fsTargetPath, sshCmd, protocol string,
	fileSize int64, status int) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	ev := fsEvent{
		Timestamp:    timestamp.UTC().Format(time.RFC3339Nano),
		Action:       action,
		Username:     username,
		FsPath:       fsPath,
		FsTargetPath: fsTargetPath,
		Protocol:     protocol,
		FileSize:     fileSize,
		Status:       status,
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
			"fs path", fsPath, "error", err)
		panic(err)
	}
	return nil
}

func (n *pubSubNotifier) NotifyUserEvent(timestamp time.Time, action string, user []byte) error {
	ctx, cancelFn := context.WithDeadline(context.Background(), time.Now().Add(n.timeout))
	defer cancelFn()

	err := n.topic.Send(ctx, &pubsub.Message{
		Body: user,
		Metadata: map[string]string{
			"action":    action,
			"timestamp": timestamp.UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		appLogger.Warn("unable to publish user event to topic", "action", action, "error", err)
		panic(err)
	}
	return nil
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

	topicUrl := os.Args[1]
	appLogger.Info("starting sftpgo-plugin-pubsub", "version", getVersionString(), "topic", topicUrl)

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
				topic:   topic,
				timeout: 30 * time.Second,
			}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
