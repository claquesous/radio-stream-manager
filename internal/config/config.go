package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AWS     AWSConfig     `yaml:"aws"`
	Ices    IcesConfig    `yaml:"ices"`
	Icecast IcecastConfig `yaml:"icecast"`
	API     APIConfig     `yaml:"api"`
	Logging LoggingConfig `yaml:"logging"`
}

type AWSConfig struct {
	Region       string `yaml:"region"`
	SQSQueueURL  string `yaml:"sqs_queue_url"`
	DynamoDBTable string `yaml:"dynamodb_table"`
}

type IcesConfig struct {
	BinaryPath string `yaml:"binary_path"`
	ConfigDir  string `yaml:"config_dir"`
	LogDir     string `yaml:"log_dir"`
}

type IcecastConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
}

type APIConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout string `yaml:"timeout"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func Load() (*Config, error) {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "/etc/radio-stream-manager/config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Override with environment variables
	if val := os.Getenv("AWS_REGION"); val != "" {
		config.AWS.Region = val
	}
	if val := os.Getenv("STREAM_EVENTS_QUEUE_URL"); val != "" {
		config.AWS.SQSQueueURL = val
	}
	if val := os.Getenv("DYNAMODB_TABLE"); val != "" {
		config.AWS.DynamoDBTable = val
	}
	if val := os.Getenv("API_BASE_URL"); val != "" {
		config.API.BaseURL = val
	}
	if val := os.Getenv("ICECAST_HOST"); val != "" {
		config.Icecast.Host = val
	}
	if val := os.Getenv("ICECAST_PORT"); val != "" {
		config.Icecast.Port = val
	}
	if val := os.Getenv("ICECAST_PASSWORD"); val != "" {
		config.Icecast.Password = val
	}

	return &config, nil
}
