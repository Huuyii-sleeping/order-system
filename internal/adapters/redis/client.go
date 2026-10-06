// Package redis 提供 Redis 基础设施适配器。
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var ErrCacheMiss = errors.New("redis cache miss")

// Client 把第三方 Redis 客户端收敛在适配器内部，避免应用层依赖具体 SDK。
type Client struct {
	client *goredis.Client
}

func Open(ctx context.Context, redisURL string) (*Client, error) {
	options, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	options.DialTimeout = 2 * time.Second
	options.ReadTimeout = 500 * time.Millisecond
	options.WriteTimeout = 500 * time.Millisecond

	client := goredis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{client: client}, nil
}

func (c *Client) PingContext(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, ErrCacheMiss
	}
	return value, err
}

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

var incrementWindowScript = goredis.NewScript(`
local count = redis.call('INCR', KEYS[1])
local ttl
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
else
  ttl = redis.call('PTTL', KEYS[1])
  if ttl < 0 then
    redis.call('PEXPIRE', KEYS[1], ARGV[1])
    ttl = tonumber(ARGV[1])
  end
end
return {count, ttl}
`)

// IncrementWindow 使用一段 Lua 脚本把计数和首次设置过期时间合并为一个
// Redis 原子操作，避免两个应用实例之间出现 INCR/EXPIRE 竞态。
func (c *Client) IncrementWindow(
	ctx context.Context, key string, window time.Duration,
) (count int64, remaining time.Duration, err error) {
	windowMilliseconds := window.Milliseconds()
	if windowMilliseconds < 1 {
		windowMilliseconds = 1
	}
	values, err := incrementWindowScript.Run(
		ctx, c.client, []string{key}, windowMilliseconds,
	).Int64Slice()
	if err != nil {
		return 0, 0, err
	}
	if len(values) != 2 {
		return 0, 0, fmt.Errorf("unexpected fixed-window result length %d", len(values))
	}
	return values[0], time.Duration(values[1]) * time.Millisecond, nil
}
