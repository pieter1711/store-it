// Package storeit provides a thin, dependency-injection-friendly wrapper
// around go-redis for storing and retrieving arbitrary objects as JSON.
//
// The store itself (RedisStore) is a plain, non-generic struct so it can be
// constructed once and injected anywhere (via constructor injection, wire,
// fx, etc.) just like a *sql.DB or *redis.Client. Go does not allow generic
// methods on a type, so the generic, type-safe behavior lives in package
// level functions (Set, Get) that take the store as their first argument.
package storeit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound is returned when a key does not exist in Redis.
var ErrNotFound = errors.New("redisstore: key not found")

// RedisStore is a thin, non-generic wrapper around a *redis.Client.
//
// It is intentionally not generic so a single *RedisStore instance can be
// created at startup and injected into any service that needs Redis access,
// regardless of what object types that service stores. Type safety is
// added at the call site via the generic Set/Get functions below.
type RedisStore struct {
	client *redis.Client
}

// Config holds the connection options for NewRedisStore.
type Config struct {
	Addr     string // e.g. "localhost:6379"
	Password string // "" if none
	DB       int    // database index, default 0
}

// NewRedisStore is a wrapper/init function that builds a *redis.Client from
// Config, verifies connectivity with a PING, and returns a ready-to-inject
// *RedisStore.
func NewRedisStore(ctx context.Context, cfg Config) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redisstore: failed to connect to redis: %w", err)
	}

	return &RedisStore{client: client}, nil
}

// NewRedisStoreFromClient wraps an existing *redis.Client. Useful when the
// client is already constructed/configured elsewhere (e.g. shared across
// multiple stores, or set up by a DI container) and you just want to plug
// it into RedisStore.
func NewRedisStoreFromClient(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

// Close closes the underlying Redis client connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// Client exposes the underlying *redis.Client, for cases where a caller
// needs raw Redis commands not covered by this wrapper.
func (s *RedisStore) Client() *redis.Client {
	return s.client
}

// Delete removes the value stored under key. It does not error if the key
// doesn't exist.
func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redisstore: delete failed: %w", err)
	}
	return nil
}

// Exists reports whether key is currently stored.
func (s *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("redisstore: exists check failed: %w", err)
	}
	return n > 0, nil
}

// Set marshals value as JSON and stores it under key. ttl of 0 means no expiration.
//
//	err := store.Set(ctx, "user:1", user, time.Hour)
func (s *RedisStore) Set[T any](ctx context.Context, key string, value T, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redisstore: marshal failed: %w", err)
	}
	if err := s.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redisstore: set failed: %w", err)
	}
	return nil
}

// Get retrieves the value stored under key and unmarshals it into T.
// Returns ErrNotFound if the key does not exist.
//
//	user, err := store.Get[User](ctx, "user:1")
func (s *RedisStore) Get[T any](ctx context.Context, key string) (T, error) {
	var zero T

	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return zero, ErrNotFound
		}
		return zero, fmt.Errorf("redisstore: get failed: %w", err)
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return zero, fmt.Errorf("redisstore: unmarshal failed: %w", err)
	}
	return value, nil
}

// GetMany retrieves multiple values of type T in a single round trip using
// MGET. Missing keys are simply omitted from the returned map (no error).
//
//	users, err := store.GetMany[User](ctx, []string{"user:1", "user:2"})
func (s *RedisStore) GetMany[T any](ctx context.Context, keys []string) (map[string]T, error) {
	if len(keys) == 0 {
		return map[string]T{}, nil
	}

	results, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redisstore: mget failed: %w", err)
	}

	out := make(map[string]T, len(keys))
	for i, r := range results {
		if r == nil {
			continue
		}
		str, ok := r.(string)
		if !ok {
			continue
		}
		var value T
		if err := json.Unmarshal([]byte(str), &value); err != nil {
			return nil, fmt.Errorf("redisstore: unmarshal failed for key %q: %w", keys[i], err)
		}
		out[keys[i]] = value
	}
	return out, nil
}
