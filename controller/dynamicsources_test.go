package controller_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller"
)

func TestDynamicWatchEventHandlerRoutesSameNamespaceResourceToParent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Instance scheme: %v", err)
	}
	parent := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "rune"},
		Status: appsv1.InstanceStatus{Conditions: []metav1.Condition{{
			Type:   appsv1.ConditionInstalled,
			Status: metav1.ConditionTrue,
		}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent).Build()
	resource := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      "database-config",
		Namespace: parent.Namespace,
		Labels: map[string]string{
			apps.LabelInstance:          parent.Name,
			apps.LabelInstanceNamespace: parent.Namespace,
		},
	}}

	request, ok := routeCreateEvent(t, reader, resource)
	if !ok {
		t.Fatal("same-namespace resource event did not enqueue its parent Instance")
	}
	if request.NamespacedName != client.ObjectKeyFromObject(parent) {
		t.Fatalf("request = %s, want %s", request.NamespacedName, client.ObjectKeyFromObject(parent))
	}
}

func TestDynamicWatchEventHandlerRoutesCrossNamespaceResourceToParent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Instance scheme: %v", err)
	}
	parent := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "scheduler", Namespace: "rune-system"},
		Status: appsv1.InstanceStatus{Conditions: []metav1.Condition{{
			Type:   appsv1.ConditionInstalled,
			Status: metav1.ConditionTrue,
		}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent).Build()
	resource := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      "volcano-config",
		Namespace: "volcano-system",
		Labels: map[string]string{
			apps.LabelInstance:          parent.Name,
			apps.LabelInstanceNamespace: parent.Namespace,
		},
	}}

	request, ok := routeCreateEvent(t, reader, resource)
	if !ok {
		t.Fatal("cross-namespace resource event did not enqueue its parent Instance")
	}
	if request.NamespacedName != client.ObjectKeyFromObject(parent) {
		t.Fatalf("request = %s, want %s", request.NamespacedName, client.ObjectKeyFromObject(parent))
	}
}

func TestDynamicWatchEventHandlerRoutesClusterScopedResourceToParent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Instance scheme: %v", err)
	}
	parent := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "cloud-agent", Namespace: "rune-system"},
		Status: appsv1.InstanceStatus{Conditions: []metav1.Condition{{
			Type:   appsv1.ConditionInstalled,
			Status: metav1.ConditionTrue,
		}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent).Build()
	resource := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{
		Name: "cloud-agent",
		Labels: map[string]string{
			apps.LabelInstance:          parent.Name,
			apps.LabelInstanceNamespace: parent.Namespace,
		},
	}}

	request, ok := routeCreateEvent(t, reader, resource)
	if !ok {
		t.Fatal("cluster-scoped resource event did not enqueue its parent Instance")
	}
	if request.NamespacedName != client.ObjectKeyFromObject(parent) {
		t.Fatalf("request = %s, want %s", request.NamespacedName, client.ObjectKeyFromObject(parent))
	}
}

func routeCreateEvent(t *testing.T, reader client.Client, object client.Object) (reconcile.Request, bool) {
	t.Helper()
	queue := workqueue.NewTypedRateLimitingQueue(
		workqueue.DefaultTypedControllerRateLimiter[reconcile.Request](),
	)
	t.Cleanup(queue.ShutDown)
	handler := (controller.DynamicWatchEventHandler{Client: reader}).Handler()
	handler.Create(t.Context(), event.CreateEvent{Object: object}, queue)
	if queue.Len() == 0 {
		return reconcile.Request{}, false
	}
	request, shutdown := queue.Get()
	if shutdown {
		return reconcile.Request{}, false
	}
	queue.Done(request)
	queue.Forget(request)
	return request, true
}

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
				Labels: map[string]string{
					apps.LabelInstance:          parent.Name,
					apps.LabelInstanceNamespace: parent.Namespace,
				},
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
