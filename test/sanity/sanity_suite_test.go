package sanity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/api/marketplace/v2"
	"github.com/scaleway/scaleway-sdk-go/scw"

	"golang.org/x/crypto/ssh"
)

func TestSanity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sanity Suite")
}

var (
	// sshPublicKey (authorized key).
	sshPublicKey string
	// ssh signer.
	sshSigner ssh.Signer

	instanceAPI    *instance.API
	marketplaceAPI *marketplace.API
)

var _ = BeforeSuite(func() {
	// Create SSH keypair to access the instances that will be created.
	signer, authorization, err := createSSHKeypair()
	Expect(err).NotTo(HaveOccurred())

	sshPublicKey = authorization
	sshSigner = signer

	// Make sure necessary env vars are present.
	Expect(os.Getenv(scw.ScwDefaultZoneEnv)).ToNot(BeEmpty(), "Please set SCW_DEFAULT_ZONE")
	Expect(os.Getenv(scw.ScwDefaultProjectIDEnv)).ToNot(BeEmpty(), "Please set SCW_DEFAULT_PROJECT_ID")
	Expect(os.Getenv(scw.ScwAccessKeyEnv)).ToNot(BeEmpty(), "Please set SCW_ACCESS_KEY")
	Expect(os.Getenv(scw.ScwSecretKeyEnv)).ToNot(BeEmpty(), "Please set SCW_SECRET_KEY")

	// Create SCW client.
	scwClient, err := scw.NewClient(scw.WithEnv())
	Expect(err).NotTo(HaveOccurred())

	// Create APIs.
	instanceAPI = instance.NewAPI(scwClient)
	marketplaceAPI = marketplace.NewAPI(scwClient)
})

// createSSHKeypair returns an SSH signer for use with the SSH client and the
// corresponding authorized key.
func createSSHKeypair() (signer ssh.Signer, authorization string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create SSH public key: %w", err)
	}

	authorization = strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")

	b, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	signer, err = ssh.ParsePrivateKey(pem.EncodeToMemory(
		&pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: b,
		},
	))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse SSH private key: %w", err)
	}

	return
}
