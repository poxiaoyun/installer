package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller"
)

var _ = Describe("Dynamic resource watches", func() {
	It("keeps watching after the reconcile that registered the kind ends", func() {
		watchScheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(watchScheme)).To(Succeed())
		Expect(appsv1.AddToScheme(watchScheme)).To(Succeed())

		parent := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "dynamic-watch-parent", Namespace: "default"},
			Status: appsv1.InstanceStatus{Conditions: []metav1.Condition{{
				Type:   appsv1.ConditionInstalled,
				Status: metav1.ConditionTrue,
			}}},
		}
		reader := contextCheckingClient{Client: fake.NewClientBuilder().
			WithScheme(watchScheme).
			WithObjects(parent).
			Build()}

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
			(controller.DynamicWatchEventHandler{Client: reader}).Handler(),
			predicate.ResourceVersionChangedPredicate{},
		)
		Expect(sources.Start(controllerCtx, queue)).To(Succeed())

		reconcileCtx, finishReconcile := context.WithCancel(controllerCtx)
		Expect(sources.Watch(reconcileCtx, corev1.SchemeGroupVersion.WithKind("ConfigMap"))).To(Succeed())

		resource := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dynamic-watch-resource",
				Namespace: "default",
				Labels:    map[string]string{apps.LabelInstance: parent.Name},
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
		Expect(request.NamespacedName).To(Equal(client.ObjectKeyFromObject(parent)))

		finishReconcile()
		resource.Data["state"] = "healthy"
		Expect(k8sClient.Update(ctx, resource)).To(Succeed())

		Eventually(queue.Len, 3*time.Second, 50*time.Millisecond).Should(Equal(1))
	})
})

// contextCheckingClient models the real cached client boundary: reads issued
// by an event handler must fail once the handler's context is cancelled.
type contextCheckingClient struct {
	client.Client
}

func (c contextCheckingClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
