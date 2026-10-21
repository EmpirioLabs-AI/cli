// Package release verifies that a release's SHA256SUMS was signed by the
// EmpirioLabs AI release key.
//
// Signatures use the SSH signature format (PROTOCOL.sshsig), so anyone can
// check one with the ssh-keygen that macOS, Linux and Windows already ship:
//
//	ssh-keygen -Y verify -f allowed_signers -I release@empiriolabs.ai \
//	  -n empirio-release -s SHA256SUMS.sig < SHA256SUMS
package release

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Namespace binds a signature to this purpose; a signature made for anything
// else with the same key does not verify.
const Namespace = "empirio-release"

// Signer is the identity in allowed_signers.
const Signer = "release@empiriolabs.ai"

// AllowedSigners lists the keys a release may be signed with, in ssh-keygen's
// allowed_signers format. The installers carry the same keys (a test checks).
//
//go:embed allowed_signers
var AllowedSigners string

// TrustedKeys parses AllowedSigners.
func TrustedKeys() ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	sc := bufio.NewScanner(strings.NewReader(AllowedSigners))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != Signer || fields[1] != `namespaces="`+Namespace+`"` {
			return nil, fmt.Errorf("allowed_signers: unexpected line %q", line)
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.Join(fields[2:], " ")))
		if err != nil {
			return nil, fmt.Errorf("allowed_signers: %w", err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("allowed_signers: no keys")
	}
	return keys, nil
}

type sigBlob struct {
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

type signedData struct {
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Hash          []byte
}

const magic = "SSHSIG"

// Digest hashes message the way an SSH signature does.
func Digest(algorithm string, message []byte) ([]byte, error) {
	switch algorithm {
	case "sha512":
		sum := sha512.Sum512(message)
		return sum[:], nil
	case "sha256":
		sum := sha256.Sum256(message)
		return sum[:], nil
	}
	return nil, fmt.Errorf("unsupported hash %q", algorithm)
}

// SignedBytes is what the key signs for message under namespace.
func SignedBytes(namespace, algorithm string, message []byte) ([]byte, error) {
	h, err := Digest(algorithm, message)
	if err != nil {
		return nil, err
	}
	return append([]byte(magic), ssh.Marshal(signedData{Namespace: namespace, HashAlgorithm: algorithm, Hash: h})...), nil
}

// Armor wraps a signature blob the way ssh-keygen writes SHA256SUMS.sig.
func Armor(blob []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b bytes.Buffer
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n-----END SSH SIGNATURE-----\n")
	return b.Bytes()
}

// Blob encodes a signature over message by key, as ssh-keygen -Y sign does.
func Blob(key ssh.PublicKey, namespace, algorithm string, sig *ssh.Signature) []byte {
	var version [4]byte
	version[3] = 1
	return append(append([]byte(magic), version[:]...), ssh.Marshal(sigBlob{
		PublicKey: key.Marshal(), Namespace: namespace, HashAlgorithm: algorithm, Signature: ssh.Marshal(sig),
	})...)
}

// Verify checks that armored is a signature over message, for Namespace, by
// one of the trusted keys.
func Verify(message, armored []byte, trusted []ssh.PublicKey) error {
	text := strings.TrimSpace(strings.ReplaceAll(string(armored), "\r\n", "\n"))
	const begin, end = "-----BEGIN SSH SIGNATURE-----", "-----END SSH SIGNATURE-----"
	if !strings.HasPrefix(text, begin) || !strings.HasSuffix(text, end) {
		return errors.New("the signature is not an SSH signature")
	}
	body := strings.Join(strings.Fields(text[len(begin):len(text)-len(end)]), "")
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(raw) < len(magic)+4 || string(raw[:len(magic)]) != magic {
		return errors.New("the signature could not be read")
	}
	if raw[6] != 0 || raw[7] != 0 || raw[8] != 0 || raw[9] != 1 {
		return errors.New("the signature has an unknown version")
	}
	var blob sigBlob
	if err := ssh.Unmarshal(raw[len(magic)+4:], &blob); err != nil {
		return errors.New("the signature could not be read")
	}
	if blob.Namespace != Namespace {
		return fmt.Errorf("the signature is for %q, not a release", blob.Namespace)
	}
	var key ssh.PublicKey
	for _, k := range trusted {
		if bytes.Equal(k.Marshal(), blob.PublicKey) {
			key = k
		}
	}
	if key == nil {
		return errors.New("the release is not signed by the EmpirioLabs AI release key")
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(blob.Signature, &sig); err != nil {
		return errors.New("the signature could not be read")
	}
	if sig.Format != key.Type() {
		return errors.New("the signature does not match its key")
	}
	data, err := SignedBytes(blob.Namespace, blob.HashAlgorithm, message)
	if err != nil {
		return err
	}
	if err := key.Verify(data, &sig); err != nil {
		return errors.New("the signature does not match the file")
	}
	return nil
}
