# Installer

A controller manage helm charts and kustomize in kubernetes operator way.

## Features

- **Helm / Kustomize / Template** three deployment modes via `Instance` CR
- **Post-rendering pipeline**: namespace enforcement, instance identity, opt-in extensions, pause control, lifecycle strategies, dashboard resources
- **Permission control**: cluster-scoped and cross-namespace resources are denied by default; allow per namespace via startup flag `--allow-cluster-scoped-namespaces` or annotation `apps.xiaoshiai.cn/allow-cluster-scoped: "true"`
- **Common metadata extension**: explicitly injects `values.global.commonLabels` and `values.global.commonAnnotations` into resources and Pod templates; `app.kubernetes.io/instance` is always enforced independently
- **Raw manifest extension**: append YAML or JSON Kubernetes objects through ordered `RawManifest` extensions; generated objects pass through the same namespace, identity, and pause enforcement
- **Dependency management**: instance dependencies via `spec.dependencies`
- **Values from external sources**: reference ConfigMap / Secret via `spec.valuesFrom`
- **Immutable chart artifacts**: install Helm charts from a same-namespace immutable Secret with SHA-256 verification
- **Scale, pause, and resume**: `spec.replicas` is exposed through the Kubernetes scale subresource and injected as `values.global.replicas`; scale status reports the current non-terminal Pods selected by the instance label plus the optional `app.kubernetes.io/scale-pod-selector` annotation; the independent `values.global.paused` control pauses Deployment, StatefulSet, Job, CronJob, and DaemonSet
- **Workload status tracking**: endpoints, states, and summary are computed from managed resources with CEL expressions supplied through `Instance` annotations
- **Lifecycle strategies**: per-resource upgrade `Retain` / `Recreate` and remove `Retain`

## Installation

```sh
kubectl create namespace rune-system
kubectl apply -f install.yaml
```

## Example

Install a Helm chart from an immutable Secret delivered by Apps:

```yaml
apiVersion: apps.xiaoshiai.cn/v1
kind: Instance
metadata:
  name: my-nginx
spec:
  kind: helm
  artifact:
    secretRef:
      name: my-nginx-10.2.1
      key: chart.tgz
    digest: sha256:<chart.tgz-sha256> # optional
  values:
    ingress:
      enabled: true
```

The referenced Secret must be in the Instance namespace, have type
`apps.xiaoshiai.cn/helm-chart.v1`, and set `immutable: true`. The digest in the
Instance and the `apps.xiaoshiai.cn/content-digest` annotation are optional;
when present, each is verified against the selected Secret data. `secretRef.key`
may select any non-empty data key.

Legacy URL-based sources remain supported:

```sh
cat <<EOF | kubectl apply -f -
apiVersion: apps.xiaoshiai.cn/v1
kind: Instance
metadata:
  name: my-nginx # helm release name
spec:
  kind: helm
  url: oci://registry-1.docker.io/bitnamicharts/nginx
  version: 10.2.1
  values: # helm chart values
    ingress:
      enabled: true
EOF
```

Append additional resources with an ordered `RawManifest` extension:

```yaml
spec:
  extensions:
    - name: default-network-policy
      kind: RawManifest
      params:
        manifest: |
          apiVersion: networking.k8s.io/v1
          kind: NetworkPolicy
          metadata:
            name: default-deny
          spec:
            podSelector: {}
            policyTypes: [Ingress, Egress]
```

The `manifest` parameter accepts a multi-document YAML or JSON stream. Extension
entries execute in declaration order; their final objects then receive the
instance namespace, identity labels, and pause handling.

Check the status of the helm instance

```sh
$ kubectl get instances.apps.xiaoshiai.cn
NAME       STATUS      NAMESPACE   VERSION   UPGRADETIMESTAMP   AGE
my-nginx   Installed   default     10.2.1    2s                 2s
```

## Contributing

Contributions are welcome! Please open issues and submit pull requests for any features, bug fixes, or improvements.
Architecture, ownership, and durable invariants are documented in [DESIGN.md](DESIGN.md).

## License

[License](License)
