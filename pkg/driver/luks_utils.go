package driver

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	cryptsetupCmd     = "cryptsetup"
	defaultLuksHash   = "sha256"
	defaultLuksCipher = "aes-xts-plain64"
	defaultLuksKeyize = "256"
)

func luksFormat(devicePath string, passphrase string) error {
	args := []string{
		"-q",                      // don't ask for confirmation
		"luksFormat",              // format
		"--hash", defaultLuksHash, // hash algorithm
		"--cipher", defaultLuksCipher, // the cipher used
		"--key-size", defaultLuksKeyize, // the size of the encryption key
		devicePath,                 // device to encrypt
		"--key-file", "/dev/stdin", // read the passphrase from stdin
	}

	luksFormatCmd := exec.Command(cryptsetupCmd, args...)
	luksFormatCmd.Stdin = strings.NewReader(passphrase)

	if err := luksFormatCmd.Run(); err != nil {
		return fmt.Errorf("luksFormat failed: %w", err)
	}

	return nil
}

func luksOpen(devicePath string, mapperFile string, passphrase string) error {
	args := []string{
		"luksOpen",                 // open
		devicePath,                 // device to open
		mapperFile,                 // mapper file in which to open the device
		"--key-file", "/dev/stdin", // read the passphrase from stdin
	}

	luksOpenCmd := exec.Command(cryptsetupCmd, args...)
	luksOpenCmd.Stdin = strings.NewReader(passphrase)

	if err := luksOpenCmd.Run(); err != nil {
		return fmt.Errorf("luksOpen failed: %w", err)
	}

	return nil
}

func luksClose(mapperFile string) error {
	args := []string{
		"luksClose", // close
		mapperFile,  // mapper file to close
	}

	luksCloseCmd := exec.Command(cryptsetupCmd, args...)

	if err := luksCloseCmd.Run(); err != nil {
		return fmt.Errorf("luksClose failed: %w", err)
	}

	return nil
}

func luksResize(mapperFile, passphrase string) error {
	args := []string{
		"resize",                   // resize
		mapperFile,                 // mapper file to resize
		"--key-file", "/dev/stdin", // read the passphrase from stdin
	}

	luksResizeCmd := exec.Command(cryptsetupCmd, args...)

	luksResizeCmd.Stdin = strings.NewReader(passphrase)
	o := &bytes.Buffer{}
	e := &bytes.Buffer{}
	luksResizeCmd.Stdout = o
	luksResizeCmd.Stderr = e

	if err := luksResizeCmd.Run(); err != nil {
		return fmt.Errorf("luks resize failed: %s, stdout: %s, stderr: %s", err, o.String(), e.String())
	}
	return nil
}

func luksStatus(mapperFile string) ([]byte, error) {
	args := []string{
		"status",   // status
		mapperFile, // mapper file to get status
	}

	var stdout bytes.Buffer

	luksStatusCmd := exec.Command(cryptsetupCmd, args...)
	luksStatusCmd.Stdout = &stdout

	if err := luksStatusCmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to get luks status: %w", err)
	}

	return stdout.Bytes(), nil
}

func luksIsLuks(devicePath string) (bool, error) {
	args := []string{
		"isLuks",   // isLuks
		devicePath, // device path to check
	}

	luksIsLuksCmd := exec.Command(cryptsetupCmd, args...)

	if err := luksIsLuksCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := errors.As(err, &exitErr); ok {
			if exitErr.ExitCode() == 1 { // not a luks device
				return false, nil
			}
		}
		return false, fmt.Errorf("failed to check if device is luks: %w", err)
	}

	return true, nil
}
