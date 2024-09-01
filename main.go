package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/hashicorp/consul/api"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Consul       ConsulConfig    `yaml:"consul"`
	AWSRegion    string          `yaml:"aws_region"`
	AWSProfile   string          `yaml:"aws_profile"`
	AWSRoleARN   string          `yaml:"aws_role_arn"`
	AWSAccessKey string          `yaml:"aws_access_key"`
	AWSSecretKey string          `yaml:"aws_secret_key"`
	Services     []ServiceConfig `yaml:"services"`
}

type ConsulConfig struct {
	Address string `yaml:"address"`
	Token   string `yaml:"token"`
}

type ServiceConfig struct {
	Name         string      `yaml:"name"`
	ConsulKVPath string      `yaml:"consul_kv_path"`
	LogConfigs   []LogConfig `yaml:"log_configs"`
	Destination  Destination `yaml:"destination"`
}

type LogConfig struct {
	LogGroupName    string `yaml:"log_group_name"`
	LogStreamPrefix string `yaml:"log_stream_prefix"`
}

type Destination struct {
	Type     string `yaml:"type"`
	FilePath string `yaml:"file_path"`
	FileName string `yaml:"file_name"`
}

func main() {
	config := loadConfig("config.yaml")
	sess := createAWSSession(config)
	consulClient := setupConsulClient(config.Consul)

	for _, service := range config.Services {
		cwLogs := cloudwatchlogs.New(sess)

		for _, logConfig := range service.LogConfigs {
			logStreams, err := listLogStreams(cwLogs, logConfig.LogGroupName, logConfig.LogStreamPrefix)
			if err != nil {
				log.Fatalf("failed to list log streams for %s: %v", service.Name, err)
			}

			for _, stream := range logStreams {
				go tailLogStream(cwLogs, service, logConfig, stream, consulClient)
			}
		}
	}

	select {}
}

func loadConfig(path string) Config {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("failed to read config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		log.Fatalf("failed to unmarshal config: %v", err)
	}
	return config
}

func setupConsulClient(consulConfig ConsulConfig) *api.Client {
	config := api.DefaultConfig()
	config.Address = consulConfig.Address
	config.Token = consulConfig.Token
	client, err := api.NewClient(config)
	if err != nil {
		log.Fatalf("failed to create consul client: %v", err)
	}
	return client
}

func createAWSSession(config Config) *session.Session {
	sessOptions := session.Options{
		Config: aws.Config{
			Region: aws.String(config.AWSRegion),
		},
	}
	// I used profile for local testing
	if config.AWSProfile != "" {
		sessOptions.Profile = config.AWSProfile
	} else if config.AWSRoleARN != "" {
		sess := session.Must(session.NewSession(&sessOptions.Config))
		creds := stscreds.NewCredentials(sess, config.AWSRoleARN)
		sessOptions.Config.Credentials = creds
	} else if config.AWSAccessKey != "" && config.AWSSecretKey != "" {
		sessOptions.Config.Credentials = credentials.NewStaticCredentials(
			config.AWSAccessKey,
			config.AWSSecretKey, "",
		)
	} else {
		log.Fatalf("No proper AWS creds provided")
	}

	return session.Must(session.NewSessionWithOptions(sessOptions))
}

func listLogStreams(cwLogs *cloudwatchlogs.CloudWatchLogs, logGroupName, logStreamPrefix string) ([]string, error) {
	var logStreams []string
	err := cwLogs.DescribeLogStreamsPages(&cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName:        aws.String(logGroupName),
		LogStreamNamePrefix: aws.String(logStreamPrefix),
	}, func(page *cloudwatchlogs.DescribeLogStreamsOutput, lastPage bool) bool {
		for _, stream := range page.LogStreams {
			if strings.HasPrefix(*stream.LogStreamName, logStreamPrefix) {
				logStreams = append(logStreams, *stream.LogStreamName)
			}
		}
		return !lastPage
	})

	if err != nil {
		return nil, err
	}
	return logStreams, nil
}

func tailLogStream(cwLogs *cloudwatchlogs.CloudWatchLogs, service ServiceConfig, logConfig LogConfig, logStreamName string, consulClient *api.Client) {
	OffsetPath := service.ConsulKVPath + "/" + logStreamName
	lastTimestamp := loadOffsetFromConsul(consulClient, OffsetPath)

	for {
		params := &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  aws.String(logConfig.LogGroupName),
			LogStreamName: aws.String(logStreamName),
			StartTime:     aws.Int64(lastTimestamp),
			StartFromHead: aws.Bool(true),
			Limit:         aws.Int64(100),
		}

		resp, err := cwLogs.GetLogEvents(params)
		if err != nil {
			log.Printf("Error getting log events for stream %s: %v", logStreamName, err)
			time.Sleep(15 * time.Second)
			continue
		}

		for _, event := range resp.Events {
			fmt.Printf("[%s] %s\n", logStreamName, *event.Message)
			lastTimestamp = *event.Timestamp
		}

		if len(resp.Events) > 0 {
			time.Sleep(5 * time.Second)
			saveOffsetToConsul(consulClient, OffsetPath, lastTimestamp)
		}

		time.Sleep(5 * time.Second)
	}
}

func saveOffsetToConsul(consulClient *api.Client, kvPath string, lastTimestamp int64) error {
	kvPair := &api.KVPair{
		Key:   kvPath,
		Value: []byte(fmt.Sprintf("%d", lastTimestamp)),
	}
	_, err := consulClient.KV().Put(kvPair, nil)
	return err
}

func loadOffsetFromConsul(consulClient *api.Client, kvPath string) int64 {
	kvPair, _, err := consulClient.KV().Get(kvPath, nil)
	if err != nil || kvPair == nil {
		fmt.Printf("Failed to load offset from consul or offset not found: %v\n", err)
		return 0
	}

	var lastTimestamp int64
	fmt.Sscanf(string(kvPair.Value), "%d", &lastTimestamp)
	return lastTimestamp
}
