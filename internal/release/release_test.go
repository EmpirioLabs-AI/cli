package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newKey(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer, signer.PublicKey()
}

func sign(t *testing.T, signer ssh.Signer, namespace string, message []byte) []byte {
	t.Helper()
	data, err := SignedBytes(namespace, "sha512", message)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.Sign(rand.Reader, data)
	if err != nil {
		t.Fatal(err)
	}
	return Armor(Blob(signer.PublicKey(), namespace, "sha512", sig))
}

func TestVerify(t *testing.T) {
	signer, pub := newKey(t)
	_, other := newKey(t)
	sums := []byte("abc  empirio_linux_amd64.tar.gz\n")
	good := sign(t, signer, Namespace, sums)
	if err := Verify(sums, good, []ssh.PublicKey{other, pub}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(append(sums, 'x'), good, []ssh.PublicKey{pub}); err == nil {
		t.Fatal("a changed file verified")
	}
	if err := Verify(sums, good, []ssh.PublicKey{other}); err == nil {
		t.Fatal("an untrusted key verified")
	}
	if err := Verify(sums, sign(t, signer, "file", sums), []ssh.PublicKey{pub}); err == nil {
		t.Fatal("a signature for another purpose verified")
	}
	if err := Verify(sums, []byte("not a signature"), []ssh.PublicKey{pub}); err == nil {
		t.Fatal("garbage verified")
	}
	crlf := []byte(strings.ReplaceAll(string(good), "\n", "\r\n"))
	if err := Verify(sums, crlf, []ssh.PublicKey{pub}); err != nil {
		t.Fatalf("CRLF line endings: %v", err)
	}
}

func TestTrustedKeysAreTheShippedKey(t *testing.T) {
	keys, err := TrustedKeys()
	if err != nil || len(keys) == 0 {
		t.Fatal(keys, err)
	}
	// The installers check the same key; a rotation must update all three.
	for _, installer := range []string{"install.sh", "install.ps1"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", installer))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if !strings.Contains(string(raw), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))) {
				t.Fatalf("%s does not carry the release key", installer)
			}
		}
	}
}

// sshKeygen finds ssh-keygen, which macOS, Linux and Windows ship.
func sshKeygen(t *testing.T) string {
	t.Helper()
	for _, p := range []string{os.Getenv("SystemRoot") + `\System32\OpenSSH\ssh-keygen.exe`, "ssh-keygen"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	t.Skip("ssh-keygen is not installed")
	return ""
}

// The format must be the one ssh-keygen writes and reads, in both directions.
func TestSSHKeygenAgrees(t *testing.T) {
	keygen := sshKeygen(t)
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	message := []byte("0123  empirio_windows_amd64.zip\n")
	os.WriteFile(sums, message, 0o644)
	if out, err := exec.Command(keygen, "-Y", "sign", "-f", key, "-n", Namespace, sums).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	pubText, _ := os.ReadFile(key + ".pub")
	pub, _, _, _, err := ssh.ParseAuthorizedKey(pubText)
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := os.ReadFile(sums + ".sig")
	if err := Verify(message, sig, []ssh.PublicKey{pub}); err != nil {
		t.Fatalf("ssh-keygen's signature: %v", err)
	}

	// And ssh-keygen accepts what this package writes.
	signer, ours := newKey(t)
	os.WriteFile(sums+".sig", sign(t, signer, Namespace, message), 0o644)
	allowed := filepath.Join(dir, "allowed_signers")
	os.WriteFile(allowed, []byte(Signer+` namespaces="`+Namespace+`" `+string(ssh.MarshalAuthorizedKey(ours))), 0o644)
	cmd := exec.Command(keygen, "-Y", "verify", "-f", allowed, "-I", Signer, "-n", Namespace, "-s", sums+".sig")
	cmd.Stdin = strings.NewReader(string(message))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen rejected our signature: %v: %s", err, out)
	}
}
