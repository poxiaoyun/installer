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
