package controller

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Ensure DynamicSources implements source.TypedSource.
var _ source.TypedSource[reconcile.Request] = &DynamicSources{}

type DynamicSources struct {
	Cache        cache.Cache
	watchedKinds map[schema.GroupVersionKind]bool
	watchedMutex sync.Mutex
	eventHandler handler.TypedEventHandler[client.Object, reconcile.Request]
	predicates   []predicate.TypedPredicate[client.Object]

	// queue is captured from Start
	queue workqueue.TypedRateLimitingInterface[reconcile.Request]
}

func NewDynamicSources(
	cache cache.Cache,
	eventHandler handler.TypedEventHandler[client.Object, reconcile.Request],
	predicates ...predicate.TypedPredicate[client.Object],
) *DynamicSources {
	return &DynamicSources{
		Cache:        cache,
		watchedKinds: map[schema.GroupVersionKind]bool{},
		eventHandler: eventHandler,
		predicates:   predicates,
	}
}

func (d *DynamicSources) Start(
	_ context.Context,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) error {
	d.watchedMutex.Lock()
	defer d.watchedMutex.Unlock()
	if d.queue != nil {
		return nil
	}
	d.queue = q
	return nil
}

func (d *DynamicSources) Watch(ctx context.Context, gvk schema.GroupVersionKind) error {
	log := logr.FromContextOrDiscard(ctx)
	d.watchedMutex.Lock()
	defer d.watchedMutex.Unlock()

	if d.watchedKinds[gvk] {
		return nil
	}
	if d.queue == nil {
		return fmt.Errorf("DynamicSources not started yet")
	}

	// Create a PartialObjectMetadata object of the correct kind to use for getting the informer
	// This reduces memory usage as we only cache metadata
	u := &metav1.PartialObjectMetadata{}
	u.SetGroupVersionKind(gvk)

	log.Info("Starting dynamic watch for kind", "kind", gvk.String())

	src := source.TypedKind[client.Object](d.Cache, u, d.eventHandler, d.predicates...)

	if err := src.Start(ctx, d.queue); err != nil {
		return fmt.Errorf("failed to start watch for %s: %w", gvk.String(), err)
	}

	d.watchedKinds[gvk] = true
	return nil
}
