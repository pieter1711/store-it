# store-it

A tiny, dependency-injection-friendly Redis wrapper for Go. Store and
retrieve any type as JSON, with full compile-time type safety via generics —
no `interface{}`, no type assertions.

```go
store, _ := storeit.NewRedisStore(ctx, storeit.Config{Addr: "localhost:6379"})

_ = storeit.Set(ctx, store, "user:1", User{ID: "1", Name: "Ada"}, time.Hour)
user, err := storeit.Get[User](ctx, store, "user:1")
```

## Why

Most Redis wrappers are either fully untyped (`interface{}` in, cast it
yourself on the way out) or generic *per struct* (`Store[User]`,
`Store[Session]`, ...), which means a new instance — and a new thing to wire
up — for every object type in your app.

store-it takes a different approach:

- **`RedisStore` is a plain, non-generic struct.** Build one at startup and
  inject it anywhere, exactly like you would a `*sql.DB` or `*redis.Client`.
- **Type safety lives at the call site**, via generic package-level
  functions (`Set[T]`, `Get[T]`, `GetMany[T]`). Go doesn't allow generic
  methods on a type, so this is the idiomatic way to combine generics with a
  single shared, injectable dependency.
- One store instance can safely hold `User`, `Session`, `Order`, whatever —
  each `Get[T]` call is fully type-checked by the compiler.

## Install

```bash
go get github.com/pieter1711/store-it
```

## Usage

### Initialize

```go
store, err := storeit.NewRedisStore(ctx, storeit.Config{
    Addr:     "localhost:6379",
    Password: "", // optional
    DB:       0,  // optional
})
if err != nil {
    log.Fatal(err)
}
defer store.Close()
```

Already have a configured `*redis.Client` (shared across your app, built by
a DI container, etc.)? Wrap it directly:

```go
store := storeit.NewRedisStoreFromClient(existingClient)
```

### Set and Get

```go
type User struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

// ttl of 0 means no expiration
err := storeit.Set(ctx, store, "user:1", User{ID: "1", Name: "Ada Lovelace"}, 10*time.Minute)

user, err := storeit.Get[User](ctx, store, "user:1")
if errors.Is(err, storeit.ErrNotFound) {
    // key doesn't exist
}
```

### Delete / Exists

```go
exists, err := store.Exists(ctx, "user:1")
err = store.Delete(ctx, "user:1")
```

### Batch fetch

```go
users, err := storeit.GetMany[User](ctx, store, []string{"user:1", "user:2"})
// missing keys are simply omitted from the result map
```

### Dependency injection

Because `RedisStore` isn't generic, one instance can be injected into any
number of services, regardless of what type each service stores:

```go
type UserService struct {
    store *storeit.RedisStore
}

func NewUserService(store *storeit.RedisStore) *UserService {
    return &UserService{store: store}
}

func (s *UserService) Save(ctx context.Context, u User) error {
    return storeit.Set(ctx, s.store, "user:"+u.ID, u, 0)
}

func (s *UserService) Find(ctx context.Context, id string) (User, error) {
    return storeit.Get[User](ctx, s.store, "user:"+id)
}
```

```go
store, _ := storeit.NewRedisStore(ctx, storeit.Config{Addr: "localhost:6379"})

users := NewUserService(store)     // reuses store for User
sessions := NewSessionService(store) // reuses the same store for Session
```

## API

| Function / Method | Description |
|---|---|
| `NewRedisStore(ctx, Config) (*RedisStore, error)` | Builds a client from config and pings it before returning. |
| `NewRedisStoreFromClient(*redis.Client) *RedisStore` | Wraps an existing client. |
| `Set[T](ctx, *RedisStore, key, value T, ttl) error` | Marshals `value` as JSON and stores it. `ttl` of `0` = no expiration. |
| `Get[T](ctx, *RedisStore, key) (T, error)` | Unmarshals the stored JSON into `T`. Returns `ErrNotFound` if missing. |
| `GetMany[T](ctx, *RedisStore, keys []string) (map[string]T, error)` | Batch fetch via `MGET`. Missing keys are omitted. |
| `(*RedisStore) Delete(ctx, key) error` | Removes a key. No error if it doesn't exist. |
| `(*RedisStore) Exists(ctx, key) (bool, error)` | Checks whether a key is set. |
| `(*RedisStore) Client() *redis.Client` | Escape hatch to the underlying client for raw commands. |
| `(*RedisStore) Close() error` | Closes the underlying connection. |

## Testing

Tests run against [`miniredis`](https://github.com/alicebob/miniredis)
(in-memory Redis), so no real Redis server is required:

```bash
go test ./...
```

## Requirements

- Go 1.22+ (generics)
- [github.com/redis/go-redis/v9](https://github.com/redis/go-redis)

## License

MIT