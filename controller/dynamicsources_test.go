package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"xiaoshiai.cn/installer/controller"
)

var _ = Describe("Dynamic resource watches", func() {
	It("keeps watching after the reconcile that registered the kind ends", func() {
		watchScheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(watchScheme)).To(Succeed())
		parent := client.ObjectKey{Namespace: "default", Name: "dynamic-watch-parent"}
		watchedGVK := corev1.SchemeGroupVersion.WithKind("ConfigMap")

		watchCache, err := cache.New(cfg, cache.Options{Scheme: watchScheme})
		Expect(err).NotTo(HaveOccurred())
		queue := workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[reconcile.Request](),
		)
		DeferCleanup(queue.ShutDown)

		controllerCtx, stopController := context.WithCancel(context.Background())
		cacheStopped := make(chan error, 1)
		go func() {
			cacheStopped <- watchCache.Start(controllerCtx)
		}()
		DeferCleanup(func() {
			stopController()
			Eventually(cacheStopped, 10*time.Second).Should(Receive(BeNil()))
		})

		sources := controller.NewDynamicSources(
			watchCache,
			func(gvk schema.GroupVersionKind) handler.TypedEventHandler[client.Object, reconcile.Request] {
				Expect(gvk).To(Equal(watchedGVK))
				return handler.TypedEnqueueRequestsFromMapFunc(func(eventCtx context.Context, _ client.Object) []reconcile.Request {
					if eventCtx.Err() != nil {
						return nil
					}
					return []reconcile.Request{{NamespacedName: parent}}
				})
			},
			predicate.ResourceVersionChangedPredicate{},
		)
		Expect(sources.Start(controllerCtx, queue)).To(Succeed())

		reconcileCtx, finishReconcile := context.WithCancel(controllerCtx)
		Expect(sources.Watch(reconcileCtx, watchedGVK)).To(Succeed())

		resource := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dynamic-watch-resource",
				Namespace: "default",
			},
			Data: map[string]string{"state": "reconciling"},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), resource) })

		Eventually(queue.Len, 10*time.Second, 50*time.Millisecond).Should(Equal(1))
		request, shutdown := queue.Get()
		Expect(shutdown).To(BeFalse())
		queue.Done(request)
		queue.Forget(request)
		Expect(request.NamespacedName).To(Equal(parent))

		finishReconcile()
		resource.Data["state"] = "healthy"
		Expect(k8sClient.Update(ctx, resource)).To(Succeed())

		Eventually(queue.Len, 3*time.Second, 50*time.Millisecond).Should(Equal(1))
	})
})
