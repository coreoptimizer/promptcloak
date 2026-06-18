package vault

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisKeyPrefix = "cmpl:tok:"

// Redis is a Redis-backed Vault. Mappings are stored with a TTL so they expire
// automatically. It is shared across replicas and survives ext_proc restarts,
// which makes reversible tokenization work behind a horizontally-scaled
// gateway.
type Redis struct {
	c   *redis.Client
	ttl time.Duration
}

// NewRedis connects to Redis and verifies connectivity with a PING.
func NewRedis(ctx context.Context, cfg RedisDial, ttl time.Duration) (*Redis, error) {
	c := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping %s: %w", cfg.Addr, err)
	}
	return &Redis{c: c, ttl: ttl}, nil
}

// RedisDial holds the connection parameters for NewRedis.
type RedisDial struct {
	Addr     string
	Password string
	DB       int
}

func (r *Redis) Put(ctx context.Context, token, value string) error {
	return r.c.Set(ctx, redisKeyPrefix+token, value, r.ttl).Err()
}

func (r *Redis) Get(ctx context.Context, token string) (string, bool, error) {
	v, err := r.c.Get(ctx, redisKeyPrefix+token).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (r *Redis) Close() error {
	return r.c.Close()
}
