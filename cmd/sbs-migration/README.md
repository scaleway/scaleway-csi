# sbs-migration

The `sbs-migration` tool migrates your Kubernetes `PersistentVolumes` from [Instance](https://console.scaleway.com/instance/volumes)
to the new [Block Storage](https://console.scaleway.com/block-storage/volumes) product. There is no downtime during the migration.
Your Snapshots will also be migrated.

**You must run this tool during the `v0.{1,2}.X` to `v0.3.X` upgrade of the Scaleway CSI.**

> [!CAUTION]
> This tool is intended to be used by customers who manage their own Kubernetes cluster.
>
> **DO NOT USE THIS TOOL IF YOUR CLUSTER IS MANAGED BY SCALEWAY (e.g. Kapsule / Kosmos)**.

## Requirements

- The kubeconfig of a Kubernetes cluster that you manage, with the Scaleway CSI installed (version `v0.1.X` or `v0.2.X`).
- [Go 1.20+](https://go.dev/dl/).
- Kubectl CLI.

## Usage

Please read all the steps before executing any command.

1. Stop the CSI controller: `$ kubectl scale deployment scaleway-csi-controller -n kube-system --replicas=0`.
2. Set the following environment variables:

   ```bash
   export SCW_DEFAULT_ZONE=fr-par-1
   export SCW_DEFAULT_PROJECT_ID=11111111-1111-1111-1111-111111111111
   export SCW_ACCESS_KEY=SCW123456789ABCDE
   export SCW_SECRET_KEY=11111111-1111-1111-1111-111111111111
   ```

3. Run the `sbs-migration` tool with dry-run enabled: `$ go run cmd/sbs-migration/main.go -kubeconfig=<path to your kubeconfig> -dry-run`.
4. If you are happy with the dry-run result, run the `sbs-migration` with dry-run disabled
   to effectively migrate your volumes and snapshots: `$ go run cmd/sbs-migration/main.go -kubeconfig=<path to your kubeconfig>`.
5. Upgrade the CSI to `v0.3.1` or higher.
