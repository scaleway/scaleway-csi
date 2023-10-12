#!/bin/bash
set -e

cd "$(dirname "$0")" # Automatically cd to the directory that contains this script.

K8S_VERSION=$(kubectl version --output json | jq -r '.serverVersion.gitVersion')
export K8S_VERSION
export KUBE_SSH_USER="${KUBE_SSH_USER:=ubuntu}"

if [[ "${K8S_VERSION}" == "null" ]]; then
    echo "ERROR: Unable to get k8s version, make sure you set the KUBECONFIG environment variable."
    exit 1
fi

curl --location https://dl.k8s.io/"${K8S_VERSION}"/kubernetes-test-linux-amd64.tar.gz | \
  tar --strip-components=3 -zxf - kubernetes/test/bin/e2e.test

./e2e.test -ginkgo.focus='External.Storage' -ginkgo.skip='Ephemeral-volume' -ginkgo.timeout=6h -storage.testdriver=test-driver.yaml
