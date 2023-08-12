// Copyright (C) 2021-2023 Nicola Murino
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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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

	"github.com/sftpgo/sdk/plugin/notifier"
)

const version = "1.0.8-dev"

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
