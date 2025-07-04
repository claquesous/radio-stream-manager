package state

import (
  "context"
  "fmt"
  "time"

  "github.com/aws/aws-sdk-go-v2/aws"
  awsconfig "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type DynamoDBManager struct {
  client    *dynamodb.Client
  tableName string
}

type StreamProcess struct {
  StreamID      int       `dynamodbav:"stream_id"`
  Status        string    `dynamodbav:"status"`
  ProcessID     int       `dynamodbav:"process_id"`
  ConfigPath    string    `dynamodbav:"config_path"`
  Mountpoint    string    `dynamodbav:"mountpoint"`
  CreatedAt     time.Time `dynamodbav:"created_at"`
  LastHeartbeat time.Time `dynamodbav:"last_heartbeat"`
  ErrorMessage  string    `dynamodbav:"error_message,omitempty"`
  Name          string    `dynamodbav:"name"`
  Premium       bool      `dynamodbav:"premium"`
  Description   string    `dynamodbav:"description"`
  Genre         string    `dynamodbav:"genre"`
}

const (
  StatusRunning = "running"
  StatusStopped = "stopped"
  StatusError   = "error"
)

func NewDynamoDBManager(region, tableName string) (*DynamoDBManager, error) {
  cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion(region))
  if err != nil {
    return nil, fmt.Errorf("failed to load AWS config: %w", err)
  }

  return &DynamoDBManager{
    client:    dynamodb.NewFromConfig(cfg),
    tableName: tableName,
  }, nil
}

func (m *DynamoDBManager) SaveProcess(ctx context.Context, process *StreamProcess) error {
  item, err := attributevalue.MarshalMap(process)
  if err != nil {
    return fmt.Errorf("failed to marshal process: %w", err)
  }

  _, err = m.client.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String(m.tableName),
    Item:      item,
  })
  if err != nil {
    return fmt.Errorf("failed to save process: %w", err)
  }

  return nil
}

func (m *DynamoDBManager) GetProcess(ctx context.Context, streamID int) (*StreamProcess, error) {
  result, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{
    TableName: aws.String(m.tableName),
    Key: map[string]types.AttributeValue{
      "stream_id": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", streamID)},
    },
  })
  if err != nil {
    return nil, fmt.Errorf("failed to get process: %w", err)
  }

  if result.Item == nil {
    return nil, nil
  }

  var process StreamProcess
  if err := attributevalue.UnmarshalMap(result.Item, &process); err != nil {
    return nil, fmt.Errorf("failed to unmarshal process: %w", err)
  }

  return &process, nil
}

func (m *DynamoDBManager) DeleteProcess(ctx context.Context, streamID int) error {
  _, err := m.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
    TableName: aws.String(m.tableName),
    Key: map[string]types.AttributeValue{
      "stream_id": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", streamID)},
    },
  })
  if err != nil {
    return fmt.Errorf("failed to delete process: %w", err)
  }

  return nil
}

func (m *DynamoDBManager) ListProcesses(ctx context.Context) ([]*StreamProcess, error) {
  result, err := m.client.Scan(ctx, &dynamodb.ScanInput{
    TableName: aws.String(m.tableName),
  })
  if err != nil {
    return nil, fmt.Errorf("failed to scan processes: %w", err)
  }

  var processes []*StreamProcess
  for _, item := range result.Items {
    var process StreamProcess
    if err := attributevalue.UnmarshalMap(item, &process); err != nil {
      continue
    }
    processes = append(processes, &process)
  }

  return processes, nil
}

func (m *DynamoDBManager) UpdateHeartbeat(ctx context.Context, streamID int) error {
  _, err := m.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
    TableName: aws.String(m.tableName),
    Key: map[string]types.AttributeValue{
      "stream_id": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", streamID)},
    },
    UpdateExpression: aws.String("SET last_heartbeat = :heartbeat"),
    ExpressionAttributeValues: map[string]types.AttributeValue{
      ":heartbeat": &types.AttributeValueMemberS{Value: time.Now().Format(time.RFC3339)},
    },
  })
  if err != nil {
    return fmt.Errorf("failed to update heartbeat: %w", err)
  }

  return nil
}
