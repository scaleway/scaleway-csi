# Sanity test

The sanity test suite is a wrapper for running the [Sanity Test Command Line Program](https://github.com/kubernetes-csi/csi-test/tree/master/cmd/csi-sanity)
against the `scaleway-csi`.

It builds the current project, runs it on a Scaleway instance and verifies that
the [sanity](https://github.com/kubernetes-csi/csi-test/tree/master/cmd/csi-sanity)
test suite succeeds.

## Requirements

- [Go >=1.20](https://go.dev/)
- [A Scaleway API key](https://www.scaleway.com/en/docs/identity-and-access-management/iam/how-to/create-api-keys/)

## Running the test

> **Warning**
> By running this test suite, you will be billed for resources created during the
> tests such as Instances, Block Volumes and Snapshots. There are no guarantees that
> these resources will be deleted automatically after the test suite completes
> (especially if it fails).

Set the following environment variables:

```console
export SCW_DEFAULT_ZONE=fr-par-1
export SCW_DEFAULT_PROJECT_ID=11111111-1111-1111-1111-111111111111
export SCW_ACCESS_KEY=SCW123456789ABCDE
export SCW_SECRET_KEY=11111111-1111-1111-1111-111111111111
```

Run the test suite:

```console
$ go test -v -timeout 10m github.com/scaleway/scaleway-csi/test/sanity
=== RUN   TestSanity
Running Suite: Sanity Suite - /home/user/dev/scaleway-csi/test/sanity
===============================================================================
Random Seed: 1693337733

Will run 1 of 1 specs
•

Ran 1 of 1 Specs in 146.095 seconds
SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 0 Skipped
--- PASS: TestSanity (146.10s)
PASS
ok      github.com/scaleway/scaleway-csi/test/sanity    146.104s
```

(Optional): Run the test suite with ginkgo:

```console
ginkgo -v test/sanity/
```
