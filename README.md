# Scaleway Block Volume CSI driver

The [Scaleway Block Volume](https://www.scaleway.com/en/block-storage/) Container Storage Interface (CSI) driver is an implementation of the [CSI interface](https://github.com/container-storage-interface/spec/blob/master/spec.md) to provide a way to manage Scaleway Block Volumes through a container orchestration system, like Kubernetes.

**WARNING**: ⚠️ This project is under active development and should be considered alpha.

### CSI Specification Compability Matrix

| Scaleway CSI Driver \ CSI Version | v1.2.0 | v1.6.0 |
|-----------------------------------|--------|--------|
| master branch                     | yes    | yes    |
| v0.1.x                            | yes    | no     |
| v0.2.x                            | yes    | yes    |

### Features

Here is a list of functionality implemented by the Scaleway CSI driver.

#### Block device resizing

The Scaleway CSI driver implements the resize feature ([example for Kubernetes](https://kubernetes.io/blog/2018/07/12/resizing-persistent-volumes-using-kubernetes/)). It allows an online resize (without the need to detach the block device). However resizing can only be done upwards, decreasing a volume's size is not supported.

#### Raw Block Volume

[Raw Block Volumes](https://kubernetes.io/blog/2019/03/07/raw-block-volume-support-to-beta/) allows the block volume to be exposed directly to the container as a block device, instead of a mounted filesystem. To enable it, the `volumeMode` needs to be set to `Block`. For instance, here is a PVC in raw block volume mode:
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-raw-pvc
spec:
  volumeMode: Block
  [...]
```

#### At-Rest Encryption

Support for volume encryption with Cryptsetup/LUKS. [See more details in examples](https://github.com/scaleway/scaleway-csi/tree/master/examples/kubernetes#encrypting-volumes)

#### Volume Snapshots

[Volume Snapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/) allows the user to create a snapshot of a specific block volume. 

#### Volume Statistics

The Scaleway CSI driver implements the [`NodeGetVolumeStats`](https://github.com/container-storage-interface/spec/blob/master/spec.md#nodegetvolumestats) CSI method. It is used to gather statistics about the used block volumes. In Kubernetes, `kubelet` exposes these metrics.

## Kubernetes

This section is Kubernetes specific. Note that Scaleway CSI driver may work for older Kubernetes versions than those announced.
The CSI driver allows to use [Persistent Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) in Kubernetes.

### Kubernetes Version Compability Matrix

| Scaleway CSI Driver \ Kubernetes Version | Min K8s Version | Max K8s Version |
|------------------------------------------|-----------------|-----------------|
| master branch                            | v1.20           | -               |
| v0.1.x                                   | v1.17           | -               |
| v0.2.x                                   | v1.20           | -               |

### Examples

Some examples are available [here](./examples/kubernetes).

### Installation

These steps will cover how to install the Scaleway CSI driver in your Kubernetes cluster, using Helm.

> **Warning**
> Please note that the manifest files provided in `deploy/kubernetes` are deprecated and
> no longer maintained.

#### Requirements

* A Kubernetes cluster running on Scaleway instances (v1.20+)
* Scaleway Project or Organization ID, Access and Secret key
* Helm v3

#### Deployment

1. Add the Scaleway Helm repository.

    ```bash
    helm repo add scaleway https://helm.scw.cloud/
    helm repo update
    ```

2. Deploy the latest release of the `scaleway-csi` Helm chart.

    ```bash
    helm upgrade --install scaleway-csi --namespace kube-system scaleway/scaleway-csi \
        --set controller.scaleway.env.SCW_DEFAULT_ZONE=fr-par-1 \
        --set controller.scaleway.env.SCW_DEFAULT_PROJECT_ID=11111111-1111-1111-1111-111111111111 \
        --set controller.scaleway.env.SCW_ACCESS_KEY=ABCDEFGHIJKLMNOPQRST \
        --set controller.scaleway.env.SCW_SECRET_KEY=11111111-1111-1111-1111-111111111111
    ```

    Review the [configuration values](https://github.com/scaleway/helm-charts/blob/master/charts/scaleway-csi/values.yaml) for the Helm chart.

3. You can now verify that the driver is running:

    ```bash
    $ kubectl get pods -n kube-system
    [...]
    scaleway-csi-controller-76897b577d-b4dgw   8/8     Running   0          3m
    scaleway-csi-node-hvkfw                    3/3     Running   0          3m
    scaleway-csi-node-jmrz2                    3/3     Running   0          3m
    [...]
    ```

    You should see the scaleway-csi-controller and the scaleway-csi-node pods.

## Development

### Build

You can build the Scaleway CSI driver executable using the following commands:
```bash
make build
```

You can build a local docker image named scaleway-csi for your current architecture using the following command:
```bash
make docker-build
```

### Test

In order to run the tests:
```bash
make test
```

### Contribute

If you are looking for a way to contribute please read the [contributing guide](./CONTRIBUTING.md)

### Code of conduct

Participation in the Kubernetes community is governed by the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md).

## Reach us

We love feedback. Feel free to reach us on [Scaleway Slack community](https://slack.scaleway.com), we are waiting for you on #k8s.

You can also join the official Kubernetes slack on #scaleway-k8s channel

You can also [raise an issue](https://github.com/scaleway/scaleway-csi/issues/new) if you think you've found a bug.
