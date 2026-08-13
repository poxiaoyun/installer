# installer

Helm chart for the installer.

## TL;DR

```console
helm install installer ./charts/installer
```

## Introduction

A controller manage helm charts and kustomize in kubernetes operator ways.

## Prerequisites

- Kubernetes 1.21+

## Installing the Chart

To install the chart:

```console
helm install installer ./charts/installer
```

The command deploys installer on the Kubernetes cluster in the default configuration.

Each Instance reconciliation is limited to 15 minutes by default. Configure
`installer.reconciliationTimeout` with a Go duration such as `20m` when Helm
operations need a different hard limit. Keep it longer than the per-Instance
Helm `timeout` option so Helm has time to persist the operation result.

The [Parameters](#parameters) section lists the parameters
that can be configured during installation.

> **Tip**: List all releases using `helm list`

## Uninstalling the Chart

To uninstall/delete the `my-release` deployment:

```console
helm delete installer
```

The command removes all the Kubernetes components associated
with the chart and deletes the release.
