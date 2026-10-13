package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	catalogapp "example.com/order-system/internal/application/catalog"
	"example.com/order-system/internal/domain/catalog"
	"golang.org/x/sync/singleflight"
)

type cacheStore interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}

type cacheMetrics interface {
	RecordCatalogCacheOperation(string)
}

// CatalogRepository 用 Cache Aside 装饰真实商品仓库。Redis 只负责加速读取，
// 因此读取或写入缓存失败时会回源，绝不把缓存当成商品事实来源。
type CatalogRepository struct {
	source      catalogapp.Repository
	cache       cacheStore
	keyPrefix   string
	ttl         time.Duration
	notFoundTTL time.Duration
	loadTimeout time.Duration
	metrics     cacheMetrics
	loads       singleflight.Group
}

type cachedProduct struct {
	Found      bool   `json:"found"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	PriceCents int64  `json:"price_cents,omitempty"`
}

func NewCatalogRepository(
	source catalogapp.Repository, cache cacheStore, keyPrefix string,
	ttl, notFoundTTL, loadTimeout time.Duration, metrics cacheMetrics,
) *CatalogRepository {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if notFoundTTL <= 0 {
		notFoundTTL = 15 * time.Second
	}
	if loadTimeout <= 0 {
		loadTimeout = 2 * time.Second
	}
	return &CatalogRepository{
		source: source, cache: cache, keyPrefix: keyPrefix, ttl: ttl,
		notFoundTTL: notFoundTTL, loadTimeout: loadTimeout, metrics: metrics,
	}
}

// List 先保持直接回源。列表缓存的失效粒度和分页策略与详情不同，不能因为
// “已经有 Redis”就把所有查询都盲目缓存。
func (r *CatalogRepository) List(ctx context.Context) ([]catalog.Product, error) {
	return r.source.List(ctx)
}

func (r *CatalogRepository) GetByID(ctx context.Context, productID string) (catalog.Product, error) {
	if err := ctx.Err(); err != nil {
		return catalog.Product{}, err
	}
	key := r.keyPrefix + ":catalog:product:" + productID
	if product, found, err := r.read(ctx, key); err == nil {
		if found {
			r.record("hit")
			return product, nil
		}
		r.record("negative_hit")
		return catalog.Product{}, catalog.ErrProductNotFound
	} else if !errors.Is(err, ErrCacheMiss) {
		r.record("read_error")
	} else {
		r.record("miss")
	}

	// 同一实例中的并发 miss 只允许一个请求回源，缓解热点 key 过期时的击穿。
	// 后端加载使用独立且有界的 Context；某个客户端断开不会让其他等待者一起失败。
	result := r.loads.DoChan(key, func() (any, error) {
		loadContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.loadTimeout)
		defer cancel()

		// 外层 miss 和进入 singleflight 之间存在调度窗口：前一个加载可能已经
		// 写回缓存并退出，而当前请求刚刚成为下一轮 leader。这里再次检查缓存，
		// 避免紧邻的两轮请求重复回源。内部检查不重复记录请求级缓存指标。
		if cached, found, err := r.read(loadContext, key); err == nil {
			if found {
				return cached, nil
			}
			return catalog.Product{}, catalog.ErrProductNotFound
		}

		product, err := r.source.GetByID(loadContext, productID)
		if errors.Is(err, catalog.ErrProductNotFound) {
			r.writeBestEffort(loadContext, key, cachedProduct{Found: false}, r.notFoundTTL)
			return catalog.Product{}, catalog.ErrProductNotFound
		}
		if err != nil {
			return catalog.Product{}, err
		}
		r.writeBestEffort(loadContext, key, cachedProduct{
			Found: true, ID: product.ID(), Name: product.Name(), PriceCents: product.PriceCents(),
		}, jitteredTTL(r.ttl))
		return product, nil
	})

	select {
	case <-ctx.Done():
		return catalog.Product{}, ctx.Err()
	case loaded := <-result:
		if loaded.Err != nil {
			return catalog.Product{}, loaded.Err
		}
		product, ok := loaded.Val.(catalog.Product)
		if !ok {
			return catalog.Product{}, errors.New("unexpected catalog cache load result")
		}
		return product, nil
	}
}

func (r *CatalogRepository) read(
	ctx context.Context, key string,
) (catalog.Product, bool, error) {
	payload, err := r.cache.Get(ctx, key)
	if err != nil {
		return catalog.Product{}, false, err
	}
	var cached cachedProduct
	if err := json.Unmarshal(payload, &cached); err != nil {
		_ = r.cache.Delete(ctx, key)
		return catalog.Product{}, false, fmt.Errorf("decode cached product: %w", err)
	}
	if !cached.Found {
		return catalog.Product{}, false, nil
	}
	product, err := catalog.NewProduct(cached.ID, cached.Name, cached.PriceCents)
	if err != nil {
		_ = r.cache.Delete(ctx, key)
		return catalog.Product{}, false, fmt.Errorf("restore cached product: %w", err)
	}
	return product, true, nil
}

func (r *CatalogRepository) writeBestEffort(
	ctx context.Context, key string, value cachedProduct, ttl time.Duration,
) {
	payload, err := json.Marshal(value)
	if err != nil {
		r.record("write_error")
		return
	}
	if err := r.cache.Set(ctx, key, payload, ttl); err != nil {
		r.record("write_error")
	}
}

func (r *CatalogRepository) record(outcome string) {
	if r.metrics != nil {
		r.metrics.RecordCatalogCacheOperation(outcome)
	}
}

// 给正常缓存时间加入 -10% 到 +10% 的抖动，避免大量商品同时过期后一起回源。
func jitteredTTL(ttl time.Duration) time.Duration {
	spread := ttl / 10
	if spread <= 0 {
		return ttl
	}
	return ttl - spread + time.Duration(rand.Int64N(int64(2*spread)+1))
}
