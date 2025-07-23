package sanity_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/sftp"
	"github.com/scaleway/scaleway-sdk-go/api/block/v1"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/api/marketplace/v2"
	"github.com/scaleway/scaleway-sdk-go/scw"
	"golang.org/x/crypto/ssh"
)

const (
	instanceCommercialType = "PLAY2-NANO"
	remoteCSIPath          = "/usr/local/bin/scaleway-csi"
	remoteSanityPath       = "/usr/local/bin/csi-sanity"
	remoteBinaryPerm       = 0700
)

var _ = Describe("Sanity", func() {
	It("should run sanity test successfully", func(ctx SpecContext) {
		By("Creating instance")
		image, err := marketplaceAPI.GetLocalImageByLabel(&marketplace.GetLocalImageByLabelRequest{
			ImageLabel:     "ubuntu_jammy",
			CommercialType: instanceCommercialType,
		}, scw.WithContext(ctx))
		Expect(err).NotTo(HaveOccurred())

		server, err := instanceAPI.CreateServer(&instance.CreateServerRequest{
			Name:              "csi-sanity",
			DynamicIPRequired: scw.BoolPtr(true),
			Image:             &image.ID,
			CommercialType:    instanceCommercialType,
			RoutedIPEnabled:   scw.BoolPtr(true),
			Tags:              []string{"AUTHORIZED_KEY=" + strings.ReplaceAll(sshPublicKey, " ", "_")},
		}, scw.WithContext(ctx))
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func(ctx SpecContext) {
			_, err = instanceAPI.ServerAction(&instance.ServerActionRequest{
				ServerID: server.Server.ID,
				Action:   instance.ServerActionPoweroff,
			}, scw.WithContext(ctx))
			Expect(err).NotTo(HaveOccurred())

			_, err = instanceAPI.WaitForServer(&instance.WaitForServerRequest{
				ServerID: server.Server.ID,
				Zone:     server.Server.Zone,
			}, scw.WithContext(ctx))
			Expect(err).ToNot(HaveOccurred())

			// On success, we expect the server to only have one volume attached (the boot volume).
			if !CurrentSpecReport().Failed() {
				Expect(server.Server.Volumes).To(HaveLen(1))
			}

			for _, volume := range server.Server.Volumes {
				Expect(volume.VolumeType).To(Equal(instance.VolumeServerVolumeTypeSbsVolume))

				_, err := instanceAPI.DetachServerVolume(&instance.DetachServerVolumeRequest{
					Zone:     server.Server.Zone,
					ServerID: server.Server.ID,
					VolumeID: volume.ID,
				}, scw.WithContext(ctx))
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() error {
					volume, err := blockAPI.GetVolume(&block.GetVolumeRequest{
						Zone:     server.Server.Zone,
						VolumeID: volume.ID,
					}, scw.WithContext(ctx))
					if err != nil {
						return fmt.Errorf("failed to get volume: %w", err)
					}

					if volume.Status != block.VolumeStatusAvailable {
						return fmt.Errorf("volume is not available: %s", volume.Status)
					}

					return nil
				}).WithContext(ctx).WithTimeout(time.Minute).ProbeEvery(time.Second).Should(Succeed())

				Expect(blockAPI.DeleteVolume(&block.DeleteVolumeRequest{
					Zone:     server.Server.Zone,
					VolumeID: volume.ID,
				}, scw.WithContext(ctx))).To(Succeed())
			}

			Expect(instanceAPI.DeleteServer(&instance.DeleteServerRequest{
				Zone:     server.Server.Zone,
				ServerID: server.Server.ID,
			}, scw.WithContext(ctx))).To(Succeed())
		})

		// Poweron server and wait for it to start.
		_, err = instanceAPI.ServerAction(&instance.ServerActionRequest{
			ServerID: server.Server.ID,
			Action:   instance.ServerActionPoweron,
		}, scw.WithContext(ctx))
		Expect(err).NotTo(HaveOccurred())
		server.Server, err = instanceAPI.WaitForServer(&instance.WaitForServerRequest{
			ServerID: server.Server.ID,
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure instance is running and has an IP.
		Expect(server.Server.State).To(Equal(instance.ServerStateRunning))
		Expect(server.Server.PublicIPs).ToNot(BeEmpty())

		By("Connecting to the instance")
		client := &sshClient{
			Address: server.Server.PublicIPs[0].Address.String(),
			Signer:  sshSigner,
		}
		Eventually(client.Open).WithContext(ctx).Should(Succeed())
		defer client.Close()

		By("Building csi driver")
		outDriver := filepath.Join(os.TempDir(), fmt.Sprintf("csi-%d", GinkgoRandomSeed()))
		Expect(goBuild(outDriver, "../../cmd/scaleway-csi/main.go")).Should(Succeed())
		DeferCleanup(func() {
			Expect(os.Remove(outDriver)).Should(Succeed())
		})

		By("Downloading csi-test repository and building csi-sanity binary")
		// Download csi-test repository as tgz.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/kubernetes-csi/csi-test/archive/refs/heads/master.tar.gz", nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		// Create temporary dir and extract repo in this dir.
		tempCSISanityDir, err := os.MkdirTemp(os.TempDir(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tempCSISanityDir)).Should(Succeed())
		})

		Expect(untargz(resp.Body, tempCSISanityDir)).Should(Succeed())

		// Build csi-sanity binary.
		outSanity := filepath.Join(tempCSISanityDir, "csi-sanity")
		Expect(goBuild(outSanity, filepath.Join(tempCSISanityDir, "csi-test-master/cmd/csi-sanity/main.go"))).Should(Succeed())

		By("Uploading binaries to instance")
		Expect(client.UploadFile(outDriver, remoteCSIPath, remoteBinaryPerm)).Should(Succeed())
		Expect(client.UploadFile(outSanity, remoteSanityPath, remoteBinaryPerm)).Should(Succeed())

		By("Running scaleway-csi on instance")
		driverEnv := map[string]string{
			scw.ScwDefaultZoneEnv:      os.Getenv(scw.ScwDefaultZoneEnv),
			scw.ScwDefaultProjectIDEnv: os.Getenv(scw.ScwDefaultProjectIDEnv),
			scw.ScwAccessKeyEnv:        os.Getenv(scw.ScwAccessKeyEnv),
			scw.ScwSecretKeyEnv:        os.Getenv(scw.ScwSecretKeyEnv),
		}

		if apiURL, ok := os.LookupEnv(scw.ScwAPIURLEnv); ok {
			driverEnv[scw.ScwAPIURLEnv] = apiURL
		}

		csiResult, err := client.RunCmd(ctx, remoteCSIPath, driverEnv)
		Expect(err).NotTo(HaveOccurred())

		By("Running csi-sanity on instance")
		sanityResult, err := client.RunCmd(ctx, fmt.Sprintf(
			"%s --csi.endpoint=unix:/tmp/csi.sock --ginkgo.no-color "+
				// Max name in test exceeds Scaleway Block Storage API max name length of 100 chars on volumes and snapshots.
				// https://github.com/kubernetes-csi/csi-test/blob/35eb219ce5e9f6476ae4a451b6760597a9547563/pkg/sanity/controller.go#L42
				`--ginkgo.skip="maximum-length name"`,
			remoteSanityPath), nil)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for results")

		select {
		case csi := <-csiResult:
			// We should never receive something unless the driver has crashed.
			Fail(fmt.Sprintf("CSI driver has crashed: %v", csi))
		case sanity := <-sanityResult:
			if sanity.Err != nil {
				Fail(fmt.Sprintf("Sanity test failed: %v", sanity))
			}

			GinkgoLogr.Info("Sanity test ran successfully", "output", sanity.Output)
		}
	})
})

// sshClient is an SSH and SFTP client.
type sshClient struct {
	Address string
	Signer  ssh.Signer

	client *ssh.Client
	sftp   *sftp.Client
}

// Open opens the SSH and SFTP connection.
func (s *sshClient) Open() (err error) {
	s.client, err = ssh.Dial("tcp", fmt.Sprintf("%s:22", s.Address), &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(s.Signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
	})
	if err != nil {
		return fmt.Errorf("failed to open ssh connection: %w", err)
	}

	s.sftp, err = sftp.NewClient(s.client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %w", err)
	}

	return nil
}

// Close closes the SSH and SFTP connection.
func (s *sshClient) Close() error {
	return errors.Join(s.sftp.Close(), s.client.Close())
}

// sshResult contains the result of the execution of an SSH command.
type sshResult struct {
	Err    error
	Output string
}

// RunCmd runs the specified command with the provided env variables.
func (s *sshClient) RunCmd(ctx context.Context, cmd string, env map[string]string) (<-chan sshResult, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}

	var envString = strings.Builder{}

	for k, v := range env {
		if envString.Len() > 0 {
			envString.WriteString(" ")
		}

		envString.WriteString(fmt.Sprintf("%s=%s", k, v))
	}

	result := make(chan sshResult)
	resultInternal := make(chan sshResult, 1)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	go func() {
		err := session.Run(fmt.Sprintf("%s %s", envString.String(), cmd))
		resultInternal <- sshResult{Err: err, Output: fmt.Sprintf("Stdout: %s\nStderr: %s", stdout.String(), stderr.String())}
		close(resultInternal)
	}()

	go func() {
		defer session.Close()
		defer close(result)
		select {
		case <-ctx.Done():
			result <- sshResult{Err: ctx.Err()}
		case r := <-resultInternal:
			result <- r
		}
	}()

	return result, nil
}

// UploadFile uploads a local file to the server.
func (s *sshClient) UploadFile(local, remote string, mode os.FileMode) error {
	lf, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer lf.Close()

	rf, err := s.sftp.Create(remote)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer rf.Close()

	if _, err := io.Copy(rf, lf); err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	if err := rf.Chmod(mode); err != nil {
		return fmt.Errorf("failed to chmod remote file: %w", err)
	}

	return nil
}

// sanitize archive file pathing from "G305: Zip Slip vulnerability"
func sanitizeArchivePath(d, t string) (v string, err error) {
	v = filepath.Join(d, t)
	if strings.HasPrefix(v, filepath.Clean(d)) {
		return v, nil
	}

	return "", fmt.Errorf("%s: %s", "content filepath is tainted", t)
}

// untargz extracts a targz from a reader to a target directory.
func untargz(r io.Reader, target string) error {
	archive, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer archive.Close()
	tarReader := tar.NewReader(archive)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("failed to read next entry: %w", err)
		}

		path, err := sanitizeArchivePath(target, header.Name)
		if err != nil {
			return err
		}

		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return fmt.Errorf("failed to create dir: %w", err)
			}
			continue
		}

		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer file.Close()

		_, err = io.Copy(file, tarReader) //nolint:gosec
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to extract file: %w", err)
		}
	}
	return nil
}

// goBuild builds a go main file.
func goBuild(outPath, mainPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, mainPath)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, "GOOS=linux", "GOARCH=amd64")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run go build command: %w", err)
	}

	return nil
}
