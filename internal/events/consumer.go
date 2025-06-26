package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"radio-stream-manager/internal/config"
	"radio-stream-manager/internal/types"
	"radio-stream-manager/internal/processes"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	awstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/zap"
)

type SQSConsumer struct {
	sqsClient      *sqs.Client
	queueURL       string
	processManager *processes.Manager
	logger         *zap.Logger
}

func NewSQSConsumer(cfg *config.Config, processManager *processes.Manager, logger *zap.Logger) (*SQSConsumer, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion(cfg.AWS.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &SQSConsumer{
		sqsClient:      sqs.NewFromConfig(awsCfg),
		queueURL:       cfg.AWS.SQSQueueURL,
		processManager: processManager,
		logger:         logger,
	}, nil
}

func (c *SQSConsumer) Start(ctx context.Context) error {
	c.logger.Info("Starting SQS consumer", zap.String("queue_url", c.queueURL))

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("SQS consumer stopping")
			return ctx.Err()
		default:
			if err := c.pollMessages(ctx); err != nil {
				c.logger.Error("Error polling messages", zap.Error(err))
				time.Sleep(5 * time.Second)
			}
		}
	}
}

func (c *SQSConsumer) pollMessages(ctx context.Context) error {
	result, err := c.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.queueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     20,
	})
	if err != nil {
		return fmt.Errorf("failed to receive messages: %w", err)
	}

	for _, message := range result.Messages {
		if err := c.processMessage(ctx, message); err != nil {
			c.logger.Error("Failed to process message",
			zap.Error(err),
			zap.String("message_id", *message.MessageId))
			continue
		}

		// Delete message after successful processing
		_, err := c.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      aws.String(c.queueURL),
			ReceiptHandle: message.ReceiptHandle,
		})
		if err != nil {
			c.logger.Error("Failed to delete message",
			zap.Error(err),
			zap.String("message_id", *message.MessageId))
		}
	}

	return nil
}

func (c *SQSConsumer) processMessage(ctx context.Context, message awstypes.Message) error {
	var event types.StreamEvent
	if err := json.Unmarshal([]byte(*message.Body), &event); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	c.logger.Info("Processing stream event",
	zap.String("event_type", event.EventType),
	zap.Int("stream_id", event.StreamID))

	switch event.EventType {
	case types.EventStreamCreated:
		return c.processManager.StartStream(ctx, event)
	case types.EventStreamDestroyed:
		return c.processManager.StopStream(ctx, event.StreamID)
	case types.EventStreamUpdated:
		return c.processManager.UpdateStream(ctx, event)
	default:
		c.logger.Warn("Unknown event type", zap.String("event_type", event.EventType))
		return nil
	}
}
