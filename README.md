# Installer

A controller manage helm charts and kustomize in kubernetes operator way.

## Features

- **Helm / Kustomize / Template** three deployment modes via `Instance` CR
- **Post-rendering pipeline**: namespace enforcement, label injection, extensions, pause control, dashboard annotation
- **Permission control**: cluster-scoped and cross-namespace resources are denied by default; allow per namespace via startup flag `--allow-cluster-scoped-namespaces` or annotation `installer.xiaoshiai.cn/allow-cluster-scoped: "true"`
- **Common labels**: injected from `values.global.commonLabels` into all rendered resources
- **Dependency management**: instance dependencies via `spec.dependencies`
- **Values from external sources**: reference ConfigMap / Secret via `spec.valuesFrom`
- **Immutable chart artifacts**: install Helm charts from a same-namespace immutable Secret with SHA-256 verification
- **Workload status tracking**: endpoints, states, summary extracted from managed resources

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

Check the status of the helm instance

```sh
$ kubectl get instances.apps.xiaoshiai.cn
NAME       STATUS      NAMESPACE   VERSION   UPGRADETIMESTAMP   AGE
my-nginx   Installed   default     10.2.1    2s                 2s
```

## Contributing

Contributions are welcome! Please open issues and submit pull requests for any features, bug fixes, or improvements.

## License

[License](License)
