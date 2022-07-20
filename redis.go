package cache

import (
	"time"

	"github.com/go-redis/redis/v9"
)

// RedisCli redis client
var RedisCli *redis.Client

// NewRedisStore 初始化redis
func NewRedisStore(size int, network, address, password string, database int) *redis.Client {
	db := redis.NewClient(&redis.Options{
		Addr:         address,
		Password:     password, // no password set
		DB:           database, // use default DB
		DialTimeout:  10 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		PoolSize:     10,
		PoolTimeout:  30 * time.Second,
	})
	return db
}
