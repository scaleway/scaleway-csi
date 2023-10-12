# Kubernetes e2e tests

This document describes how to run Kubernetes [external CSI tests](https://github.com/kubernetes/kubernetes/tree/master/test/e2e/storage/external).

> **Warning**
> By running this test suite, you will be billed for resources created during the
> tests such as Instances, Block Volumes and Snapshots. There are no guarantees that
> these resources will be deleted automatically after the test suite completes
> (especially if it fails).

## Requirements

- A Kubernetes (v1.20+) cluster running with the `scaleway-csi` already installed.
- Clone this repository locally.

## Running locally

These tests can be run [manually](#manually) or using the [convenience script](#using-the-convenience-script).

> **Note**
> Tests that simulate a situation where kubelet goes down will fail when run on
> a Kapsule cluster, due to the way the product is configured. This is not related
> to the CSI implementation.

### Manually

1. Set the `K8S_VERSION` environment variable to the version of your Kubernetes cluster:

    ```console
    export K8S_VERSION=$(kubectl version --output json | jq -r '.serverVersion.gitVersion')
    ```

2. Download the Kubernetes `e2e.test` binary that matches the version of your cluster:

    ```console
    curl --location https://dl.k8s.io/${K8S_VERSION}/kubernetes-test-linux-amd64.tar.gz | \
        tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test
    ```

3. Assuming an SSH key is present at `$HOME/.ssh/id_rsa`, some `[Disruptive]` tests
   will attempt to run SSH commands on the nodes of your cluster using this key:

    - Set the `KUBE_SSH_KEY_PATH` environment variable if you need to use another key.
      Set this to a fake path to skip these tests altogether.
    - Set the `KUBE_SSH_USER` environment variable if you want to specify the SSH user.

4. Run the tests:

    ```console
    ./e2e.test -ginkgo.focus='External.Storage' -ginkgo.skip='Ephemeral-volume' -ginkgo.timeout=6h -storage.testdriver=test-driver.yaml
    ```

### Using the convenience script

1. Review the script and run it:

    ```console
    KUBECONFIG=/path/to/kubeconfig ./e2e.sh
    ```
