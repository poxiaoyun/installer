# Installer

A controller manage helm charts and kustomize in kubernetes operator way.

## Features

- **Helm / Kustomize / Template** three deployment modes via `Instance` CR
- **Post-rendering pipeline**: namespace enforcement, label injection, extensions, pause control, dashboard annotation
- **Permission control**: cluster-scoped and cross-namespace resources are denied by default; allow per namespace via startup flag `--allow-cluster-scoped-namespaces` or annotation `installer.xiaoshiai.cn/allow-cluster-scoped: "true"`
- **Common labels**: injected from `values.global.commonLabels` into all rendered resources
- **Dependency management**: instance dependencies via `spec.dependencies`
- **Values from external sources**: reference ConfigMap / Secret via `spec.valuesFrom`
- **Workload status tracking**: endpoints, states, summary extracted from managed resources

## Installation

```sh
kubectl create namespace rune-system
kubectl apply -f install.yaml
```

## Example

Install a helm chart

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
