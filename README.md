# SFTPGo Pub/Sub plugin

![Build](https://github.com/sftpgo/sftpgo-plugin-pubsub/workflows/Build/badge.svg?branch=main&event=push)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPLv3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)

This plugin allows to send [SFTPGo](https://github.com/drakkan/sftpgo/) filesystem and user events to publish/subscribe systems. It is not meant to react to `pre-*` events. It simply forwards the configured events to an external pub/sub system.

## Configuration

The supported services can be configured within the `plugins` section of the SFTPGo configuration file: you have to set the service publish URL as first plugin argument. This is an example configuration.

```json
...
"plugins": [
    {
      "type": "notifier",
      "notifier_options": {
        "fs_events": [
          "download",
          "upload"
        ],
        "user_events": [
          "add",
          "delete"
        ],
        "retry_max_time": 60,
        "retry_queue_max_size": 1000
      },
      "cmd": "<path to sftpgo-plugin-kms>",
      "args": ["rabbit://sftpgo"],
      "sha256sum": "",
      "auto_mtls": true
    }
  ]
...
```

With the above example SFTPGo is configured to connect to [RabbitMQ](https://www.rabbitmq.com/) and publish messages to the `sftpgo` fanout exchange. The RabbitMQ’s server is discovered from the `RABBIT_SERVER_URL` environment variable (which is something like `amqp://guest:guest@localhost:5672/`).

The plugin will not start if it fails to connect to the configured service, this will prevent SFTPGo from starting.

The plugin will panic if an error occurs while publishing an event, for example because the service connection is lost. SFTPGo will automatically restart it and you can configure SFTPGo to retry failed events until they are older than a configurable time (60 seconds in the above example). This way no event is lost.

## Notifications format

The filesystem events will contain a JSON serialized struct in the message body with the following fields:

- `timestamp`, string formatted as RFC3339 with nanoseconds precision
- `action`, string, an SFTPGo supported action
- `username`
- `fs_path`, string filesystem path
- `fs_target_path`, string, included for `rename` action and `sftpgo-copy` SSH command
- `ssh_cmd`, string, included for `ssh_cmd` action
- `file_size`, integer, included for `pre-upload`, `upload`, `download`, `delete` actions if the file size is greater than `0`
- `status`, integer. 1 means no error
- `protocol`, string. Possible values are `SSH`, `SFTP`, `SCP`, `FTP`, `DAV`, `HTTP`

The user events will contain a JSON serialized user in the message body. The following fields are added as metadata:

- `timestamp`, string formatted as RFC3339 with nanoseconds precision
- `action`, string, possible values are: `add`, `update`, `delete`

## Supported services

We use [Go CDK](https://gocloud.dev/howto/pubsub/) to access several publish/subscribe systems in a portable way.

### Google Cloud Pub/Sub

To publish events to a [Google Cloud Pub/Sub](https://cloud.google.com/pubsub/docs/) topic, you have to use `gcppubsub` as URL scheme and you have to include the project ID and the topic ID within the URL, for example `gcppubsub://projects/myproject/topics/mytopic`.

This plugin will use Application Default Credentials. See [here](https://cloud.google.com/docs/authentication/production) for alternatives such as environment variables.

### Amazon Simple Notification Service

To publish events to an Amazon [Simple Notification Service](https://aws.amazon.com/sns/) (SNS) topic, you have to use `awssns` as URL scheme. The topic is identified via the Amazon Resource Name (ARN). You should specify the region query parameter to ensure your application connects to the correct region.

Here is a sample URL: `awssns:///arn:aws:sns:us-east-2:123456789012:mytopic?region=us-east-2`.

The plugin will create a default AWS Session with the `SharedConfigEnable` option enabled. See [AWS Session](https://docs.aws.amazon.com/sdk-for-go/api/aws/session/) to learn about authentication alternatives, including using environment variables.

### Amazon Simple Queue Service

To publish events to an Amazon [Simple Queue Service](https://aws.amazon.com/sqs/) (SQS) topic, you have to use an URL that closely resemble the queue URL, except the leading `https://` is replaced with `awssqs://`. You can specify the region query parameter to ensure your application connects to the correct region.

Here is a sample URL: `awssqs://sqs.us-east-2.amazonaws.com/123456789012/myqueue?region=us-east-2`.

### Azure Service Bus

To publish to an [Azure Service Bus](https://azure.microsoft.com/en-us/services/service-bus/) topic over [AMQP 1.0](https://www.amqp.org/), you have to use `azuresb` as URL scheme and the topic name as URL host.

Here is a sample URL: `azuresb://mytopic`.

We use the environment variable `SERVICEBUS_CONNECTION_STRING` to obtain the Service Bus connection string. The connection string can be obtained [from the Azure portal](https://docs.microsoft.com/en-us/azure/service-bus-messaging/service-bus-dotnet-how-to-use-topics-subscriptions#get-the-connection-string).

### RabbitMQ

To publish events to an [AMQP 0.9.1](https://www.rabbitmq.com/protocol.html) fanout exchange, the dialect of AMQP spoken by [RabbitMQ](https://www.rabbitmq.com/), you have to use `rabbit` as URL scheme and the exchange name as URL host.

Here is a sample URL: `rabbit://myexchange`.

The RabbitMQ’s server is discovered from the `RABBIT_SERVER_URL` environment variable (which is something like `amqp://guest:guest@localhost:5672/`).

### NATS

To publish events to a [NATS](https://nats.io/) subject, you have to use `nats` as URL scheme and the subject name as URL host.

Here is a sample URL: `nats://example.mysubject`.

The NATS server is discovered from the `NATS_SERVER_URL` environment variable (which is something like `nats://nats.example.com`).

### Kafka

To publish events to a [Kafka](https://kafka.apache.org/) cluster, you have to use `kafka` as URL scheme and the topic name as URL host.

Here is a sample URL: `kafka://my-topic`.

The brokers in the Kafka cluster are discovered from the `KAFKA_BROKERS` environment variable (which is a comma-delimited list of hosts, something like `1.2.3.4:9092,5.6.7.8:9092`).
