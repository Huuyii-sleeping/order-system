package redis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/order-system/internal/domain/catalog"
)

type cacheStub struct {
	mu      sync.Mutex
	values  map[string][]byte
	getErr  error
	setErr  error
	setTTLs []time.Duration
}

func (s *cacheStub) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	value, ok := s.values[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	return append([]byte(nil), value...), nil
}

func (s *cacheStub) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[key] = append([]byte(nil), value...)
	s.setTTLs = append(s.setTTLs, ttl)
	return nil
}

func (s *cacheStub) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

type catalogSourceStub struct {
	mu      sync.Mutex
	product catalog.Product
	err     error
	calls   int
	started chan struct{}
	release chan struct{}
}

func (s *catalogSourceStub) List(context.Context) ([]catalog.Product, error) {
	return []catalog.Product{s.product}, s.err
}

func (s *catalogSourceStub) GetByID(context.Context, string) (catalog.Product, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 && s.started != nil {
		close(s.started)
	}
	if s.release != nil {
		<-s.release
	}
	return s.product, s.err
}

func (s *catalogSourceStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type cacheMetricsStub struct {
	mu       sync.Mutex
	outcomes map[string]int
}

func (s *cacheMetricsStub) RecordCatalogCacheOperation(outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcomes == nil {
		s.outcomes = make(map[string]int)
	}
	s.outcomes[outcome]++
}

func TestCatalogRepositoryCachesSourceResult(t *testing.T) {
	product := mustProduct(t)
	source := &catalogSourceStub{product: product}
	cache := &cacheStub{values: make(map[string][]byte)}
	metrics := &cacheMetricsStub{}
	repository := NewCatalogRepository(
		source, cache, "test", time.Minute, 5*time.Second, time.Second, metrics,
	)

	for range 2 {
		got, err := repository.GetByID(context.Background(), product.ID())
		if err != nil || got.ID() != product.ID() {
			t.Fatalf("GetByID() = %+v, %v", got, err)
		}
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("source calls = %d, want 1", got)
	}
	if metrics.outcomes["miss"] != 1 || metrics.outcomes["hit"] != 1 {
		t.Fatalf("cache outcomes = %v, want one miss and one hit", metrics.outcomes)
	}
	if len(cache.setTTLs) != 1 || cache.setTTLs[0] < 54*time.Second || cache.setTTLs[0] > 66*time.Second {
		t.Fatalf("cache TTLs = %v, want one TTL with ten percent jitter", cache.setTTLs)
	}
}

func TestCatalogRepositoryNegativeCachesMissingProduct(t *testing.T) {
	source := &catalogSourceStub{err: catalog.ErrProductNotFound}
	cache := &cacheStub{values: make(map[string][]byte)}
	repository := NewCatalogRepository(
		source, cache, "test", time.Minute, 5*time.Second, time.Second, nil,
	)

	for range 2 {
		_, err := repository.GetByID(context.Background(), "missing")
		if !errors.Is(err, catalog.ErrProductNotFound) {
			t.Fatalf("GetByID() error = %v, want product not found", err)
		}
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("source calls = %d, want 1", got)
	}
	if len(cache.setTTLs) != 1 || cache.setTTLs[0] != 5*time.Second {
		t.Fatalf("negative cache TTLs = %v, want [5s]", cache.setTTLs)
	}
}

func TestCatalogRepositoryFallsBackWhenCacheFails(t *testing.T) {
	product := mustProduct(t)
	source := &catalogSourceStub{product: product}
	cache := &cacheStub{getErr: errors.New("redis unavailable"), setErr: errors.New("redis unavailable")}
	repository := NewCatalogRepository(
		source, cache, "test", time.Minute, 5*time.Second, time.Second, nil,
	)

	got, err := repository.GetByID(context.Background(), product.ID())
	if err != nil || got.ID() != product.ID() {
		t.Fatalf("GetByID() = %+v, %v; want source result", got, err)
	}
}

func TestCatalogRepositoryCoalescesConcurrentMisses(t *testing.T) {
	product := mustProduct(t)
	source := &catalogSourceStub{
		product: product, started: make(chan struct{}), release: make(chan struct{}),
	}
	repository := NewCatalogRepository(
		source, &cacheStub{values: make(map[string][]byte)}, "test",
		time.Minute, 5*time.Second, time.Second, nil,
	)

	const callers = 8
	errorsFound := make(chan error, callers)
	for range callers {
		go func() {
			_, err := repository.GetByID(context.Background(), product.ID())
			errorsFound <- err
		}()
	}
	<-source.started
	close(source.release)
	for range callers {
		if err := <-errorsFound; err != nil {
			t.Errorf("GetByID() error = %v", err)
		}
	}
	if got := source.callCount(); got != 1 {
		t.Fatalf("source calls = %d, want one coalesced load", got)
	}
}

func mustProduct(t *testing.T) catalog.Product {
	t.Helper()
	product, err := catalog.NewProduct("product-1", "Keyboard", 12900)
	if err != nil {
		t.Fatalf("NewProduct() error = %v", err)
	}
	return product
}
