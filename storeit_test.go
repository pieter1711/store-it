package storeit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestStore spins up an in-memory Redis (miniredis) and returns a
// *RedisStore wired to it, plus a cleanup func. No real Redis server or
// network access is required, which keeps unit tests fast and hermetic.
func newTestStore(t *testing.T) *RedisStore {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return NewRedisStoreFromClient(client)
}

type testUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func TestSetAndGet(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	want := testUser{ID: "1", Name: "Ada Lovelace", Email: "ada@example.com"}

	if err := store.Set(ctx, "user:1", want, 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := store.Get[testUser](ctx, "user:1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != want {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.Get[testUser](ctx, "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestSet_TTLExpires(t *testing.T) {
	ctx := context.Background()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	store := NewRedisStoreFromClient(client)

	u := testUser{ID: "1", Name: "Ada"}
	if err := store.Set(ctx, "user:1", u, time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// miniredis lets tests fast-forward time instead of sleeping.
	mr.FastForward(2 * time.Minute)

	_, err = store.Get[testUser](ctx, "user:1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after TTL expiry error = %v, want ErrNotFound", err)
	}
}

func TestSet_NoTTLPersists(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	u := testUser{ID: "1", Name: "Ada"}
	if err := store.Set(ctx, "user:1", u, 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	ttl := store.client.TTL(ctx, "user:1").Val()
	if ttl != -1 { // -1 means "no expiration" in Redis
		t.Errorf("TTL = %v, want no expiration (-1)", ttl)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	u := testUser{ID: "1", Name: "Ada"}
	if err := store.Set(ctx, "user:1", u, 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if err := store.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := store.Get[testUser](ctx, "user:1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
}

func TestDelete_NonExistentKeyDoesNotError(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if err := store.Delete(ctx, "does-not-exist"); err != nil {
		t.Errorf("Delete() on missing key error = %v, want nil", err)
	}
}

func TestExists(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	exists, err := store.Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Error("Exists() = true before Set(), want false")
	}

	if err := store.Set(ctx, "user:1", testUser{ID: "1"}, 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	exists, err = store.Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Error("Exists() = false after Set(), want true")
	}
}

func TestGetMany(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	users := map[string]testUser{
		"user:1": {ID: "1", Name: "Ada"},
		"user:2": {ID: "2", Name: "Alan"},
	}
	for key, u := range users {
		if err := store.Set(ctx, key, u, 0); err != nil {
			t.Fatalf("Set(%q) error = %v", key, err)
		}
	}

	// Include a key that doesn't exist to verify it's simply omitted.
	got, err := store.GetMany[testUser](ctx, []string{"user:1", "user:2", "user:missing"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("GetMany() returned %d entries, want 2", len(got))
	}
	for key, want := range users {
		if got[key] != want {
			t.Errorf("GetMany()[%q] = %+v, want %+v", key, got[key], want)
		}
	}
	if _, ok := got["user:missing"]; ok {
		t.Error("GetMany() included missing key, want omitted")
	}
}

func TestGetMany_EmptyInput(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	got, err := store.GetMany[testUser](ctx, nil)
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GetMany(nil) = %v, want empty map", got)
	}
}

// TestGenericFunctions_MultipleTypesShareOneStore exercises the core DI
// scenario: a single *RedisStore instance is reused for unrelated object
// types, with type safety enforced at each call site via the generic
// Set/Get functions rather than by the store itself.
func TestGenericFunctions_MultipleTypesShareOneStore(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	type session struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
	}

	u := testUser{ID: "1", Name: "Ada"}
	s := session{Token: "tok-abc", UserID: "1"}

	if err := store.Set(ctx, "user:1", u, 0); err != nil {
		t.Fatalf("Set(user) error = %v", err)
	}
	if err := store.Set(ctx, "session:tok-abc", s, 0); err != nil {
		t.Fatalf("Set(session) error = %v", err)
	}

	gotUser, err := store.Get[testUser](ctx, "user:1")
	if err != nil {
		t.Fatalf("Get(user) error = %v", err)
	}
	if gotUser != u {
		t.Errorf("Get(user) = %+v, want %+v", gotUser, u)
	}

	gotSession, err := store.Get[session](ctx, "session:tok-abc")
	if err != nil {
		t.Fatalf("Get(session) error = %v", err)
	}
	if gotSession != s {
		t.Errorf("Get(session) = %+v, want %+v", gotSession, s)
	}
}

func TestNewRedisStoreFromClient(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	store := NewRedisStoreFromClient(client)
	if store.Client() != client {
		t.Error("Client() did not return the injected client")
	}
}

func TestNewRedisStore_PingFailure(t *testing.T) {
	// Point at an address nothing is listening on, so Ping fails fast and
	// NewRedisStore surfaces a connection error instead of returning a
	// store that will fail on first use.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := NewRedisStore(ctx, Config{Addr: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("NewRedisStore() error = nil, want connection error")
	}
}
