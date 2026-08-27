# Installer Design

## Purpose and domain model

Installer materializes a namespaced `Instance` into Kubernetes resources and
keeps their observed runtime state on the `Instance` status.

- An **Instance** is the desired installation: source, values, dependencies,
  extensions, lifecycle options, and instance-level replica count.
- A **source** is either an immutable in-cluster chart artifact or a URL-backed
  bundle. Artifacts are supported only for Helm instances.
- A **rendered resource** is a Kubernetes object produced from a source before
  platform invariants are applied.
- A **managed resource** is a rendered resource successfully placed under the
  Instance lifecycle and recorded in `status.resources`.
- **Runtime values** are the resolved user values plus installer-owned values
  such as `global.replicas`.
- **Pause** is an execution control over managed workloads. It is independent
  from the desired replica count.

Status is observation, not desired configuration. `status.values` records the
values used by the last successful apply; states, endpoints, summary, replica
count, and conditions describe later observations of the managed resources.

## Ownership and seams

| Module | Ownership | Interface or seam |
| --- | --- | --- |
| `apis/apps/v1` | Instance wire contract and status vocabulary | Kubernetes CRD and scale subresource |
| `controller` | Reconciliation order, dependency gating, value and credential resolution, execution decisions, and status projection | `InstanceReconciler` |
| `controller/postrender` | Platform invariants over all rendered objects | `install.PostRenderer` / `ObjectModifier` |
| `install` | Installation input, result, lifecycle policy, and installer interface | `Installer` |
| `install/delegate` and `install/download` | Source selection, retrieval, verification, and installer selection | Local bundle location passed to an installer |
| `install/helm` | Helm release lifecycle | `Installer` adapter |
| `install/native` | Resource inventory diff and direct Kubernetes apply/remove | `Installer` adapter used by Kustomize and Template modes |
| `controller/instance-status.go` | Runtime phase, conditions, states, endpoints, summary, and scale observation | Instance status |
| `controller/dynamicsources.go` | Reconciliation events from managed resource kinds | Instance identity label |

The controller is the orchestration owner. Installation adapters do not infer
Instance policy, and the controller does not reproduce Helm or native apply
mechanics.

## Canonical reconciliation

One reconciliation follows this order:

1. Load the Instance and establish its finalizer.
2. Before executing a generation that has not yet installed successfully,
   observe every declared dependency. A missing or unready dependency projects
   `Waiting` with a false dependency condition and ends reconciliation
   successfully; dependency Instance changes enqueue dependents whose current
   generation has not installed successfully. Dependencies are ordering
   prerequisites, not runtime health inputs, so their later state does not
   affect an installed dependent.
3. Validate the source, resolve values and repository credentials, and create
   one `install.Instance` input.
4. Build the common post-render pipeline.
5. Skip execution only when the last successful execution still represents the
   current generation, values, extensions, and artifact identity; otherwise
   apply through the selected installer.
6. Read managed resources and project runtime status, expressions, scale
   observation, and phase.
7. Register dynamic watches for every managed resource kind.

A failed apply does not replace the last successful execution result. The
failure is exposed through phase, message, and conditions. Reconciliation is
bounded by its runtime option; cancellation is returned to the controller
runtime rather than converted into a successful status.

Deletion is a separate flow. The finalizer remains until the selected installer
successfully removes or retains its managed resources according to lifecycle
policy.

## Source and value resolution

An artifact source references a Secret in the Instance namespace. The Secret
must use the chart artifact type, be immutable, contain the selected non-empty
data key, and hold a valid Helm archive. Supplied Instance and Secret annotation
digests are independently checked against the archive. Verified bytes are
exposed through a mode-`0600` temporary file and removed after use.

URL-backed sources may resolve a local path, Git repository, archive, or Helm
repository. A versioned source may use the download cache; an unversioned source
is fetched again so “latest” is not made stale by the cache.

Values are resolved in this order:

1. referenced Secrets and ConfigMaps in declaration order;
2. inline `spec.values`;
3. installer-owned runtime values.

Later values override earlier values recursively. Nil entries are removed.
`spec.replicas` owns `global.replicas` and therefore overrides a user-supplied
value at that path. `global.paused` remains an independent user control and is
not derived from a zero replica count.

## Rendering and platform invariants

Helm, Kustomize, and Template modes converge before resources reach Kubernetes:

1. render the selected source;
2. append chart-derived dashboard resources;
3. execute declared extensions in order;
4. enforce namespace and scope permissions;
5. apply Instance identity to resources and Pod templates;
6. apply pause behavior;
7. validate and translate lifecycle policy in the installation adapter.

RawManifest resources pass through the same permission, identity, pause, and
lifecycle rules as source-rendered resources. The Instance identity label is
installer-owned because status selection and dynamic event routing depend on
it; extensions cannot opt out of it.

Namespace-scoped resources default to the Instance namespace. Cross-namespace
and cluster-scoped resources require authorization from the controller
allow-list or the namespace annotation. Authorization is decided before apply,
not delegated to individual source modes.

Inputs that affect rendered Helm manifests without appearing in values must be
represented by the post-render identity. Changing such an input requires an
identity version change so Helm cannot incorrectly reuse an old release.

## Runtime configuration ownership

An application default must not be duplicated as a Helm value merely to pass
that same value back to the binary. Helm exposes settings only when chart
installation must choose them or couple them to Kubernetes resources.

Reconciliation timeout remains a runtime option with a 15-minute application
default and a command flag for direct process invocation. The Helm chart relies
on that default and does not expose or pass a reconciliation timeout. Installer
selects its logging configuration internally, so the chart has no `logLevel`
value or `LOG_LEVEL` environment contract. Network bind addresses, leader
election, and namespace scope authorization remain chart inputs because the
deployment or related Kubernetes resources depend on them.

## Installation and lifecycle consistency

Helm mode owns a Helm release. Kustomize and Template modes render to ordinary
objects and use the native inventory diff. The native adapter creates new
objects, applies existing objects with server-side apply, and removes objects
that disappeared from the managed inventory. All modes return the resources
they actually manage.

Lifecycle annotations have the same meaning in every mode:

- upgrade `Retain` leaves an existing resource unchanged;
- upgrade `Recreate` deletes a changed resource with foreground propagation,
  waits for deletion, and then creates it again;
- remove `Retain` leaves the resource live when it disappears from a manifest
  or the Instance is deleted.

Every affected lifecycle annotation is validated before the first mutation.
Helm maps remove retention to its keep policy and implements upgrade policy in
its Kubernetes adapter; native apply enforces the same domain semantics
directly.

## Scaling, pause, and managed HPA

`spec.replicas` is the desired Instance replica count and is exposed through the
scale subresource. The controller injects it as `global.replicas`; charts decide
which workload consumes that value. Scale status counts non-terminal Pods with
the Instance identity label and, when present, the additional scale Pod
selector annotation.

Pause is controlled only by `global.paused`:

- Deployment and StatefulSet desired replicas become zero;
- Job and CronJob become suspended;
- DaemonSet receives an unsatisfiable required node affinity;
- other resource kinds remain unchanged.

A zero desired replica count alone is not pause. It remains a normal, healthy
scaled-to-zero state.

A managed HorizontalPodAutoscaler is an HPA rendered and lifecycle-managed by
the same Instance and targeting one of that Instance's Deployments or
StatefulSets. Pause does not modify or delete the HPA. When `minReplicas` is
greater than zero, setting its target's desired replicas to zero invokes
Kubernetes HPA maintenance-mode deactivation. Resume re-renders a non-zero
target and the unchanged HPA becomes active again. No HPA state or replica
snapshot is stored by Installer.

The pause guarantee for an HPA-managed target therefore requires
`minReplicas > 0`. Supporting an HPA that may scale from zero would require a
separate explicit design because Kubernetes does not apply maintenance-mode
deactivation to that case.

## Runtime observation

Managed resource events return to the owning Instance through the enforced
identity label. Watches are registered by GroupVersionKind and use metadata-only
cache objects; Pods are watched directly because scale status depends on them.

Runtime phase is derived from observed workload states, except that an explicit
pause always projects `Paused`. Expression failures have their own condition and
do not replace the independently computed runtime phase. Default observation
supports workload states and common Service, Ingress, LoadBalancer, NodePort,
and SSH endpoints; CEL annotations may replace states or endpoints and add
summary or additional endpoints.

`Waiting` means the current generation has been observed but execution is
blocked on an expected external prerequisite. `Reconciling` means installation
or update work is actively proceeding, while `Failed` is reserved for an
actual reconciliation or runtime failure.

Conditions describe independent guarantees: dependencies, successful
installation, runtime readiness, expression evaluation, and safe scale
observation. A condition must not be inferred from an unrelated phase.

## Verification rules

Behavior is tested at the narrowest owning seam:

- API and scale behavior through the Instance Kubernetes interface;
- reconciliation guarantees through `InstanceReconciler` with a real or local
  installer adapter;
- rendered invariants through `PostRenderer` / `ObjectModifier` output;
- lifecycle semantics through the Helm and native installer interfaces;
- status semantics through observable Instance status.

Changes to the Instance wire contract require regenerated CRD manifests.
Changes to a platform rendering invariant require its public rendering test,
all affected mode tests, and this document to agree.
