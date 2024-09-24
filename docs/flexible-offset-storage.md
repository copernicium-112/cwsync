# log tailing module with flexible offset storage

## overview

this module provides the ability to tail logs and store the offset of the last processed log entry. the key feature is flexibility: we can choose different systems (e.g., redis, kafka, consul) for storing offsets based on their specific needs.

to enable this flexibility, the **strategy design pattern** is used. this pattern decouples the storage mechanism from the core log tailing logic, allowing you to easily swap in different strategies for offset storage without modifying the main logic.

## features

- **log tailing**: tail logs from sources such as aws cloudwatch.
- **flexible offset storage**: plug-and-play support for different backend storage systems like redis, kafka, consul, or any future system.
- **extensibility**: new storage backends can be added with minimal changes to the core code.
- **error handling**: handles scenarios where no new logs are available and includes retries with backoff.

## design pattern: strategy pattern

the [**strategy pattern**](https://en.wikipedia.org/wiki/Strategy_pattern) allows us to encapsulate different storage mechanisms into separate and interchangeable strategies. this design makes it possible to use different systems to load and save offsets without altering the main functionality of the module.

### why use the strategy pattern?

1. **flexibility**: different storage mechanisms can be swapped without changing the core logic.
2. **extensibility**: adding support for new systems only requires creating a new strategy that implements the same interface.
3. **separation of concerns**: the log tailing logic is decoupled from the storage logic, making the code more modular and easier to maintain.

### structure

the core components of the strategy pattern in this use case include:
- **offsetstore interface**: defines the contract for storing and loading offsets.
- **concrete strategies**: implementations of `offsetstore` for different backends (e.g., redis, kafka, consul).
- **log tailing function**: uses the `offsetstore` interface to load and save offsets without knowing which backend is being used.

## how it works

1. **offsetstore interface**: this interface defines methods to load and save offsets.
2. **concrete implementations**: you can have different implementations of `offsetstore` for redis, kafka, consul, or any other storage system.
3. **log tailing function**: the function that tails logs interacts with the `offsetstore` interface. it doesn't care which backend is being used—just that it can load and save offsets.

``` bash
+---------------------------+     +-----------------------+
|     log tailing logic     |---->|     offsetstore       |
+---------------------------+     +-----------------------+
                                     |       |       |
         +----------------------+    |       |       |
         |  redisoffsetstore    |----+       |       |
         +----------------------+            |       |
                                             |       |
         +----------------------+            |       |
         |  kafkaoffsetstore    |------------+       |
         +----------------------+                    |
                                                     |
         +----------------------+                    |
         |  consuloffsetstore   |--------------------+
         +----------------------+

```

### interface definition

``` go
// offsetstore defines the interface for loading and saving offsets.
type OffsetStore interface {
    LoadOffset(path string, fallbackDuration time.Duration) (int64, error)
    SaveOffset(path string, offset int64) error
}
```

### example implementations

#### redisoffsetstore

``` go
type RedisOffsetStore struct {
    Client *redis.Client
}

func (r *RedisOffsetStore) LoadOffset(path string, fallbackDuration time.Duration) (int64, error) {
    val, err := r.Client.Get(path).Result()
    if (err != nil) {
        return time.Now().Add(-fallbackDuration).UnixMilli(), err
    }
    return strconv.ParseInt(val, 10, 64)
}

func (r *RedisOffsetStore) SaveOffset(path string, offset int64) error {
    return r.Client.Set(path, strconv.FormatInt(offset, 10), 0).Err()
}
```

#### consuloffsetstore

``` go
type ConsulOffsetStore struct {
    Client *api.Client
}

func (c *ConsulOffsetStore) LoadOffset(path string, fallbackDuration time.Duration) (int64, error) {
    kvPair, _, err := c.Client.KV().Get(path, nil)
    if (err != nil || kvPair == nil) {
        return time.Now().Add(-fallbackDuration).UnixMilli(), err
    }
    return strconv.ParseInt(string(kvPair.Value), 10, 64)
}

func (c *ConsulOffsetStore) SaveOffset(path string, offset int64) error {
    kv := &api.KVPair{
        Key:   path,
        Value: []byte(strconv.FormatInt(offset, 10)),
    }
    _, err := c.Client.KV().Put(kv, nil)
    return err
}
```

#### kafkaoffsetstore

``` go
type KafkaOffsetStore struct {
    Producer sarama.SyncProducer
    ConsumerGroup string
}

func (k *KafkaOffsetStore) LoadOffset(path string, fallbackDuration time.Duration) (int64, error) {
    return 0, nil // placeholder implementation
}

func (k *KafkaOffsetStore) SaveOffset(path string, offset int64) error {
    return nil // placeholder implementation
}
```

### factory method to select the storage backend

you can use a factory method to select which storage backend to use based on configuration or environment variables.

``` go
func NewOffsetStore(storeType string, config interface{}) (OffsetStore, error) {
    switch storeType {
    case "consul":
        consulClient := config.(*api.Client)
        return &ConsulOffsetStore{Client: consulClient}, nil
    case "redis":
        redisClient := config.(*redis.Client)
        return &RedisOffsetStore{Client: redisClient}, nil
    case "kafka":
        kafkaConfig := config.(KafkaConfig)
        return &KafkaOffsetStore{
            Producer: kafkaConfig.Producer, 
            ConsumerGroup: kafkaConfig.ConsumerGroup,
        }, nil
    default:
        return nil, fmt.Errorf("unsupported store type: %s", storeType)
    }
}
```

### usage example

``` go
func main() {
    storeType := "consul" // example: could be "redis", "kafka", etc.
    
    offsetStore, err := NewOffsetStore(storeType, consulClient)
    if err != nil {
        log.Fatalf("failed to initialize offset store: %v", err)
    }

    tailLogStream(cwLogs, service, logConfig, "example-log-stream", offsetStore, 1*time.Hour)
}
```

## extending the module

to add support for a new offset storage system, follow these steps:

1. **implement the `offsetstore` interface**: create a new struct for the backend and implement the `LoadOffset` and `SaveOffset` methods.
2. **update the factory**: modify the factory method to return the new implementation based on the configuration.
3. **test**: ensure that the new implementation correctly loads and saves offsets.

## future improvements
- **batch processing**: support for batching multiple log events before storing the offset, improving efficiency.
- **error handling**: more robust error handling with retries and exponential backoff.

