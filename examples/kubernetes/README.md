# Kubernetes examples

You can find in this directory some examples about how to use the Scaleway CSI driver inside Kubernetes.

It will cover [Persistent Volumes/Persistent Volume Claim (PV/PVC)](https://kubernetes.io/docs/concepts/storage/persistent-volumes/), [Storage Class](https://kubernetes.io/docs/concepts/storage/storage-classes/) and [Volume Snapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/).

If a [StorageClass](https://kubernetes.io/docs/concepts/storage/storage-classes/) is not provided in the examples, the `scw-bssd` storage class will be used.

## PVC & Deployment

We will create a [PersistentVolumeClaim](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) and use it as a volume inside the pod of a deployment, to store nginx's logs.
First, we will create a 3Gi volume:
```bash
$ kubectl apply -f pvc-deployment/pvc.yaml
```

Now we can create the deployment that will use this volume:
```bash
$ kubectl apply -f pvc-deployment/deployment.yaml
```

## Raw Block Volumes

We will create a block volume and make it available in the pod as a raw block device. In order to do so, `volumeMode` must be set to `Block`.
First, create the volume:
```bash
$ kubectl apply -f raw-volume/pvc.yaml
```

Now we can create a pod that will use this raw volume. In order to do so, `volumesDevices` must be used, instead of the traditional `volumeMounts`:
```bash
$ kubectl apply -f raw-volume/pod.yaml
```

You can now exec into the container and use the volume as a classic block device:
```bash
$ kubectl exec -it my-awesome-block-volume-app sh
/ # ls -al /dev/xvda
brw-rw----    1 root     disk        8,  32 Mar 23 12:34 /dev/xvda
/ # dd if=/dev/zero of=/dev/xvda bs=1024k count=100
100+0 records in
100+0 records out
104857600 bytes (100.0MB) copied, 0.043702 seconds, 2.2GB/s
```

## Importing existing Scaleway volumes

If you have an already existing volume, with the ID `11111111-1111-1111-111111111111` in the zone `fr-par-1`, you can import it by creating the following PV:
```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: test-pv
spec:
  capacity:
    storage: 5Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteOnce
  storageClassName: scw-bssd
  csi:
    driver: csi.scaleway.com
    volumeHandle: fr-par-1/11111111-1111-1111-111111111111
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: topology.csi.scaleway.com/zone
          operator: In
          values:
          - fr-par-1
```

Once the PV is created, create a PVC with the same attributes (here `scw-bssd` as storage class and a size of 5Gi):
```bash
$ kubectl apply -f importing/pvc.yaml
```

And finally create a pod that uses this volume:
```bash
$ kubectl apply -f importing/pod.yaml
```

## Volume Snapshots

In Kubernetes, it is possible to create snapshots via the [VolumeSnapshot](https://kubernetes.io/docs/concepts/storage/volume-snapshots/) object.

Volumes can be snapshotted, and restored from snapshots. First let's create a PVC:
```bash
$ kubctl apply -f snapshots/pvc.yaml
```

We can now snapshot it:
```bash
$ kubectl apply -f snapshots/snapshot.yaml
```

And it should be marked as ready:
```bash
$ kubectl get volumesnapshot.snapshot.storage.k8s.io
NAME          READYTOUSE   SOURCEPVC                       SOURCESNAPSHOTCONTENT   RESTORESIZE   SNAPSHOTCLASS   SNAPSHOTCONTENT                                    CREATIONTIME   AGE
my-snapshot   true         my-soon-to-be-snapshotted-pvc                           5Gi           scw-snapshot    snapcontent-7146128a-c9f2-4050-856f-8ae1590eb436   113s           114s
```

When ready, you can create a new PVC from this snapshot:
```bash
$ kubectl apply -f snapshots/restored-snapshot.yaml
```

### Importing snapshots

It is also possible, as for the volumes, to import snapshots. Let's say you have a snapshot in `fr-par-1` with the ID `11111111-1111-1111-111111111111`. You must first import the `VolumeSnapshotContent` as followed:
```yaml
apiVersion: snapshot.storage.k8s.io/v1beta1
kind: VolumeSnapshotContent
metadata:
  name: my-imported-snapshot-content
spec:
  volumeSnapshotRef:
    kind: VolumeSnapshot
    name: my-imported-snapshot
  source:
    snapshotHandle: fr-par-1/11111111-1111-1111-111111111111
  driver: csi.scaleway.com
  volumeSnapshotClassName: scw-snapshot
  deletionPolicy: Retain
```

Once it's done, we can now create the `VolumeSnapshot` with:
```yaml
apiVersion: snapshot.storage.k8s.io/v1beta1
kind: VolumeSnapshot
metadata:
  name: my-imported-snapshot
spec:
  volumeSnapshotClassName: scw-snapshot
  source:
    volumeSnapshotContentName: my-imported-snapshot-content
```

And now, it's available to use:
```bash
$ kubectl get volumesnapshot.snapshot.storage.k8s.io 
NAME                   READYTOUSE   SOURCEPVC                       SOURCESNAPSHOTCONTENT          RESTORESIZE   SNAPSHOTCLASS   SNAPSHOTCONTENT                                    CREATIONTIME   AGE
my-imported-snapshot   true                                         my-imported-snapshot-content   3Gi           scw-snapshot    my-imported-snapshot-content                       46d            3m32s
```

## Different StorageClass

[StorageClasses](https://kubernetes.io/docs/concepts/storage/storage-classes/) offer a way to easily create different types of [Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/).

In the installation guide, a basic storage class is deployed: `scw-bssd` that will provision standard Scaleway Block Volumes. We will see here how to customize different storage class.
The provisioner will always be `csi.scaleway.com`.

### Set a default storage class

In order to have a default storage class (ie not having to specify the `storageClassName` for each PVC), you must add the `storageclass.kubernetes.io/is-default-class: "true"` annotation to the storage class:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: my-default-storage-class
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: csi.scaleway.com 
reclaimPolicy: Delete
```

### Choose the filesystem type

In order to change the filesystem type used to format the volume (it's `ext4` by default), you must add the `csi.storage.k8s.io/fstype` parameter to the storage class:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: my-ext3-storage-class
provisioner: csi.scaleway.com 
reclaimPolicy: Delete
parameters:
  csi.storage.k8s.io/fstype: ext3
```

### Choose the type of Scaleway Block Volume

When a new type of Scaleway Block Volume will be available, let's say it's called `b_ssd+`, you will need to add the `type` parameter to the storage class:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: my-bssdplus-storage-class
provisioner: csi.scaleway.com 
reclaimPolicy: Delete
parameters:
  type: b_ssd+
```

### Specify in which zone the volumes are going to be created

By default, the Scaleway CSI plugin uses the `SCW_DEFAULT_ZONE` environment variable to get the zone where the volumes will be provisioned.
If you want to override this value, you must use the `allowedTopologies` field of the storage class to specify a zone:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: my-ams-storage-class
provisioner: csi.scaleway.com 
reclaimPolicy: Delete
allowedTopologies:
- matchLabelExpressions:
  - key: topology.csi.scaleway.com/zone
    values:
    - nl-ams-1
```

## Encrypting Volumes

This plugin supports at rest encryption of the volumes with Cryptsetup/LUKS.

**Note that resizing an encrypted volume does not work (https://github.com/container-storage-interface/spec/issues/445)**

### Storage Class parameters

In order to have an encrypted volume, `encrypted: true` needs to be added to the StorageClass parameters.
You will also need a passphrase to encrypt/decrypt the volume, which is taken from the secrets passed to the `NodeStageVolume` method.

The [external-provisioner](https://github.com/kubernetes-csi/external-provisioner) can be used to [pass down the wanted secret to the CSI plugin](https://kubernetes-csi.github.io/docs/secrets-and-credentials-storage-class.html) (v1.0.1+).

Two additional parameters are needed on the StorageClass:
- `csi.storage.k8s.io/node-stage-secret-name`: The name of the secret
- `csi.storage.k8s.io/node-stage-secret-namespace`: The namespace of the secret

The secret needs to have the passphrase in the entry with the key `encryptionPassphrase`.

For instance with the following secret:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: enc-secret
  namespace: default
type: Opaque
data:
  encryptionPassphrase: bXlhd2Vzb21lcGFzc3BocmFzZQ==
```

and the following StorageClass:
```yaml
# Volume expansion is supported with CSINodeExpandSecret feature gate since v1.25.0 or by default since v1.27.0
allowVolumeExpansion: true
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: "scw-bssd-enc"
provisioner: csi.scaleway.com
reclaimPolicy: Delete
volumeBindingMode: Immediate
parameters:
  encrypted: "true"
  csi.storage.k8s.io/node-stage-secret-name: "enc-secret"
  csi.storage.k8s.io/node-stage-secret-namespace: "default"
  # Required for volume expansion
  csi.storage.k8s.io/node-expand-secret-name: "enc-secret"
  csi.storage.k8s.io/node-expand-secret-namespace: "default"
```

all the PVC created with the StorageClass `scw-bssd-enc` will be encrypted at rest with the passphrase `myawesomepassphrase`.

The [Per Volume Secret](https://kubernetes-csi.github.io/docs/secrets-and-credentials-storage-class.html#per-volume-secrets) can also be used to avoid having one passphrase per StorageClass.
