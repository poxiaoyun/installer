# Installer

A controller manage helm charts and kustomize in kubernetes operator way.

## Features

- **Helm / Kustomize / Template** three deployment modes via `Instance` CR
- **Post-rendering pipeline**: namespace enforcement, instance identity, opt-in extensions, pause control, lifecycle strategies, dashboard resources
- **Permission control**: cluster-scoped and cross-namespace resources are denied by default; allow per namespace via startup flag `--allow-cluster-scoped-namespaces` or annotation `apps.xiaoshiai.cn/allow-cluster-scoped: "true"`
- **Common metadata extension**: explicitly injects `values.global.commonLabels` and `values.global.commonAnnotations` into resources and Pod templates; `app.kubernetes.io/instance` is always enforced independently
- **Raw manifest extension**: append YAML or JSON Kubernetes objects through ordered `RawManifest` extensions; generated objects pass through the same namespace, identity, and pause enforcement
- **Dependency management**: `spec.dependencies` gates execution of a new Instance generation; unmet prerequisites project `Waiting`, dependency Instance updates wake dependents whose current generation has not installed successfully, and a periodic retry prevents lost events from blocking forever; later dependency health changes do not affect an already installed generation
- **Values from external sources**: reference ConfigMap / Secret via `spec.valuesFrom`
- **Immutable source artifacts**: install source data from a same-namespace immutable Secret with SHA-256 verification
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

The referenced Secret must be in the Instance namespace and set
`immutable: true`; its Kubernetes Secret type is not restricted. The digest in
the Instance and the `apps.xiaoshiai.cn/content-digest` annotation are optional;
when present, each is verified against the selected Secret data. `secretRef.key`
may select any non-empty data key. When both `artifact` and `url` are present,
the Artifact source takes precedence and URL-related settings are ignored. The
selected installer validates the data format; the Secret type itself does not
imply a format. Use a descriptive key such as `chart.tgz` or `bundle.tgz`.

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

URL sources support bearer tokens and basic authentication. A bearer token
takes precedence when both forms are configured:

```yaml
spec:
  auth:
    token: example-token
```

Credentials may also come from `auth.secretRef`. Opaque and BasicAuth Secrets
use the `token`, `username`, and `password` keys; Docker config Secrets resolve
registry/identity tokens or username and password for the source registry.

TLS certificates are verified by default for every HTTPS URL source, including
Git, archive, Helm repository, and OCI downloads. Add a private CA from a Secret when the source uses a
certificate that is not trusted by the system roots:

```yaml
spec:
  kind: helm
  url: oci://registry.example.com/charts/my-chart
  tls:
    secretRef:
      name: source-ca
```

The referenced Secret must be in the Instance namespace. It may contain
`ca.crt` with CA certificates and, when client certificate authentication is
required, both `tls.crt` and `tls.key`. A standard TLS Secret containing only
`tls.crt` and `tls.key` is also accepted; when `ca.crt` is absent, `tls.crt` is
trusted for that source. As a last resort, a trusted repository can explicitly
disable certificate verification:

```yaml
spec:
  kind: kustomize
  url: https://source.example.com/app.tgz
  tls:
    insecureSkipVerify: true
```

Skipping certificate verification weakens transport security and should only be
used when the source is otherwise trusted.

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
