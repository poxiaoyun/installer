package postrender

import "testing"

func TestInstanceIdentityRendererCarriesParentNameAndNamespace(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: child
  namespace: child-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: workload
  namespace: child-system
spec:
  template:
    metadata: {}
    spec:
      containers: []
`)

	renderer := &InstanceIdentityRenderer{
		InstanceName:      "parent",
		InstanceNamespace: "parent-system",
	}
	got, err := renderer.ModifyObjects(objects)
	if err != nil {
		t.Fatalf("ModifyObjects() error = %v", err)
	}

	for _, obj := range got {
		assertStringMapValue(t, obj.GetLabels(), "app.kubernetes.io/instance", "parent", obj.GetKind()+" labels")
		assertStringMapValue(t, obj.GetLabels(), "apps.xiaoshiai.cn/instance-namespace", "parent-system", obj.GetKind()+" labels")
	}
	templateLabels := nestedStringMap(t, objectByName(t, got, "workload").Object, "spec", "template", "metadata", "labels")
	assertStringMapValue(t, templateLabels, "app.kubernetes.io/instance", "parent", "Pod template labels")
	assertStringMapValue(t, templateLabels, "apps.xiaoshiai.cn/instance-namespace", "parent-system", "Pod template labels")
}
