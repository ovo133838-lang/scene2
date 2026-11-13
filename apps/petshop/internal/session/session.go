// Package session 提供登录会话存储：Redis（生产，go-zero redis 客户端）与
// 内存（单元测试，不依赖外部组件）两种实现，接口一致。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

var ErrNoSession = errors.New("会话不存在或已过期")

// Store 会话存储接口。
type Store interface {
	Set(token string, userID int64, ttl time.Duration) error
	Get(token string) (int64, error)
	Del(token string) error
}

// NewToken 生成安全随机会话令牌。
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 不可用属环境级故障
	}
	return hex.EncodeToString(b)
}

// ---------- 内存实现（测试） ----------

type memEntry struct {
	userID  int64
	expires time.Time
}

// MemoryStore 内存会话存储，仅用于单元测试。
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]memEntry
}

func NewMemory() *MemoryStore { return &MemoryStore{m: map[string]memEntry{}} }

func (s *MemoryStore) Set(token string, userID int64, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[token] = memEntry{userID: userID, expires: time.Now().Add(ttl)}
	return nil
}

func (s *MemoryStore) Get(token string) (int64, error) {
	s.mu.RLock()
	e, ok := s.m[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(e.expires) {
		return 0, ErrNoSession
	}
	return e.userID, nil
}

func (s *MemoryStore) Del(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
	return nil
}

// ---------- Redis 实现（生产） ----------

// RedisStore 基于 go-zero core/stores/redis 的会话存储。
type RedisStore struct {
	r   *redis.Redis
	ttl time.Duration
}

// NewRedis 构造 Redis 会话存储；addr 形如 127.0.0.1:6379。
func NewRedis(addr, pass string, ttl time.Duration) (*RedisStore, error) {
	r, err := redis.NewRedis(redis.RedisConf{Host: addr, Type: "node", Pass: pass})
	if err != nil {
		return nil, fmt.Errorf("redis connect %s: %w", addr, err)
	}
	return &RedisStore{r: r, ttl: ttl}, nil
}

func (s *RedisStore) key(token string) string { return "petshop:session:" + token }

func (s *RedisStore) Set(token string, userID int64, ttl time.Duration) error {
	return s.r.Setex(s.key(token), fmt.Sprintf("%d", userID), int(ttl.Seconds()))
}

func (s *RedisStore) Get(token string) (int64, error) {
	v, err := s.r.Get(s.key(token))
	if err != nil {
		return 0, err
	}
	if v == "" {
		return 0, ErrNoSession
	}
	var id int64
	if _, err := fmt.Sscanf(v, "%d", &id); err != nil {
		return 0, ErrNoSession
	}
	return id, nil
}

func (s *RedisStore) Del(token string) error {
	_, err := s.r.Del(s.key(token))
	return err
}
