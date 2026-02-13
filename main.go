package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/plugin"
	"github.com/charmbracelet/lipgloss"
	bip39 "github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/term"
)

const x25519Label = "age-encryption.org/v1/X25519"

var b64 = base64.RawStdEncoding.Strict()

func main() {
	p, err := plugin.New("bip39")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var keygen bool
	p.RegisterFlags(nil)
	flag.BoolVar(&keygen, "k", false, "generate a new identity from a BIP39 seed phrase")
	flag.Parse()

	if keygen {
		if err := runKeygen(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if !hasFlag("age-plugin") {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  age-plugin-bip39 -k    Generate an identity from a BIP39 seed phrase\n")
		fmt.Fprintf(os.Stderr, "\nThis plugin is invoked automatically by age during decryption.\n")
		fmt.Fprintf(os.Stderr, "See https://github.com/pinpox/age-plugin-bip39 for details.\n")
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n")
		fmt.Fprintf(os.Stderr, "  AGE_PLUGIN_BIP39_CACHE  Cache TTL for derived keys (default: 10m, 0 to disable)\n")
		os.Exit(0)
	}

	p.HandleIdentity(func(data []byte) (age.Identity, error) {
		if len(data) != 32 {
			return nil, fmt.Errorf("invalid identity data length: %d", len(data))
		}
		return &Bip39Identity{plugin: p, publicKey: data}, nil
	})

	p.HandleIdentityAsRecipient(func(data []byte) (age.Recipient, error) {
		if len(data) != 32 {
			return nil, fmt.Errorf("invalid identity data length: %d", len(data))
		}
		return &Bip39Recipient{publicKey: data}, nil
	})

	os.Exit(p.Main())
}

func runKeygen() error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return runKeygenNonInteractive()
	}
	return runKeygenInteractive()
}

func runKeygenNonInteractive() error {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read seed phrase: %w", err)
	}
	mnemonic := strings.TrimSpace(string(b))
	if mnemonic == "" {
		return fmt.Errorf("no mnemonic provided on stdin")
	}
	if !bip39.IsMnemonicValid(mnemonic) {
		return fmt.Errorf("invalid BIP39 mnemonic")
	}
	return outputIdentity(mnemonic)
}

func runKeygenInteractive() error {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return fmt.Errorf("failed to generate entropy: %w", err)
	}
	generated, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return fmt.Errorf("failed to generate mnemonic: %w", err)
	}

	words := strings.Split(generated, " ")
	mnemonic, err := runWordGrid(words)
	if err != nil {
		return err
	}

	return outputIdentity(mnemonic)
}

func outputIdentity(mnemonic string) error {
	privKey, pubKey, err := deriveX25519FromMnemonic(mnemonic)
	if err != nil {
		return fmt.Errorf("key derivation failed: %w", err)
	}
	_ = privKey

	ecdhPub, err := ecdh.X25519().NewPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("failed to create ECDH public key: %w", err)
	}

	recipient, err := plugin.EncodeX25519Recipient(ecdhPub)
	if err != nil {
		return fmt.Errorf("failed to encode recipient: %w", err)
	}

	identity := plugin.EncodeIdentity("bip39", pubKey)
	if identity == "" {
		return fmt.Errorf("failed to encode identity")
	}

	// Print styled summary to stderr if interactive.
	fd := int(os.Stderr.Fd())
	if term.IsTerminal(fd) {
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
		label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
		fmt.Fprintf(os.Stderr, "\n%s\n\n", box.Render(
			label.Render("Public Key")+"  "+recipient,
		))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Printf("# created: %s\n", now)
	fmt.Printf("# public key: %s\n", recipient)
	fmt.Println(identity)
	return nil
}

// deriveX25519FromMnemonic derives an X25519 keypair from a BIP39 mnemonic
// using melt's mechanism: entropy = Ed25519 seed, then SHA-512(seed)[:32]
// gives the X25519 private key (same as ssh-to-age).
func deriveX25519FromMnemonic(mnemonic string) (privateKey, publicKey []byte, err error) {
	entropy, err := bip39.EntropyFromMnemonic(mnemonic)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract entropy: %w", err)
	}

	// Ed25519 seed = entropy bytes
	// X25519 private key = SHA-512(Ed25519 seed)[:32] (matches ssh-to-age)
	h := sha512.Sum512(entropy)
	x25519Private := make([]byte, 32)
	copy(x25519Private, h[:32])

	// Clamp the scalar (standard X25519 practice, matches Ed25519 key expansion)
	x25519Private[0] &= 248
	x25519Private[31] &= 127
	x25519Private[31] |= 64

	pubBytes, err := curve25519.X25519(x25519Private, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("X25519 scalar multiplication failed: %w", err)
	}

	return x25519Private, pubBytes, nil
}

// Bip39Identity implements age.Identity for seed-phrase-derived keys.
// The identity file stores only the public key; the private key is
// derived on-demand from the user's seed phrase.
type Bip39Identity struct {
	plugin    *plugin.Plugin
	publicKey []byte // X25519 public key (32 bytes)
}

func (si *Bip39Identity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	hasX25519 := false
	for _, s := range stanzas {
		if s.Type == "X25519" {
			hasX25519 = true
			break
		}
	}
	if !hasX25519 {
		return nil, age.ErrIncorrectIdentity
	}

	cacheKeyName := fmt.Sprintf("age-plugin-bip39:%x", si.publicKey)
	privKey := getCachedKey(cacheKeyName)

	if privKey == nil {
		mnemonic, err := si.plugin.RequestValue("Enter your BIP39 seed phrase", true)
		if err != nil {
			return nil, fmt.Errorf("failed to request seed phrase: %w", err)
		}

		mnemonic = strings.TrimSpace(mnemonic)
		if !bip39.IsMnemonicValid(mnemonic) {
			return nil, fmt.Errorf("invalid BIP39 mnemonic")
		}

		derivedPriv, derivedPub, err := deriveX25519FromMnemonic(mnemonic)
		if err != nil {
			return nil, fmt.Errorf("key derivation failed: %w", err)
		}

		if !bytesEqual(derivedPub, si.publicKey) {
			return nil, fmt.Errorf("seed phrase does not match this identity")
		}

		privKey = derivedPriv
		cacheKey(cacheKeyName, privKey)
	}

	for _, stanza := range stanzas {
		if stanza.Type != "X25519" {
			continue
		}
		fileKey, err := unwrapX25519(privKey, si.publicKey, stanza)
		if err != nil {
			continue
		}
		return fileKey, nil
	}

	return nil, age.ErrIncorrectIdentity
}

// Bip39Recipient implements age.Recipient using a stored public key.
type Bip39Recipient struct {
	publicKey []byte // X25519 public key (32 bytes)
}

func (sr *Bip39Recipient) Wrap(fileKey []byte) ([]*age.Stanza, error) {
	return wrapX25519(sr.publicKey, fileKey)
}

func unwrapX25519(secretKey, ourPublicKey []byte, stanza *age.Stanza) ([]byte, error) {
	if len(stanza.Args) != 1 {
		return nil, errors.New("invalid X25519 stanza")
	}

	ephemeralPub, err := b64.DecodeString(stanza.Args[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode ephemeral public key: %w", err)
	}
	if len(ephemeralPub) != 32 {
		return nil, errors.New("invalid ephemeral public key length")
	}

	sharedSecret, err := curve25519.X25519(secretKey, ephemeralPub)
	if err != nil {
		return nil, fmt.Errorf("X25519 failed: %w", err)
	}

	salt := make([]byte, 0, 64)
	salt = append(salt, ephemeralPub...)
	salt = append(salt, ourPublicKey...)
	h := hkdf.New(sha256.New, sharedSecret, salt, []byte(x25519Label))
	wrappingKey := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(h, wrappingKey); err != nil {
		return nil, err
	}

	return aeadDecrypt(wrappingKey, stanza.Body)
}

func wrapX25519(recipientPubKey, fileKey []byte) ([]*age.Stanza, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	ephemeralPub := ephemeral.PublicKey().Bytes()

	sharedSecret, err := curve25519.X25519(ephemeral.Bytes(), recipientPubKey)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, 0, 64)
	salt = append(salt, ephemeralPub...)
	salt = append(salt, recipientPubKey...)
	h := hkdf.New(sha256.New, sharedSecret, salt, []byte(x25519Label))
	wrappingKey := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(h, wrappingKey); err != nil {
		return nil, err
	}

	wrappedKey, err := aeadEncrypt(wrappingKey, fileKey)
	if err != nil {
		return nil, err
	}

	s := &age.Stanza{
		Type: "X25519",
		Args: []string{b64.EncodeToString(ephemeralPub)},
		Body: wrappedKey,
	}
	return []*age.Stanza{s}, nil
}

func aeadEncrypt(key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSize)
	return aead.Seal(nil, nonce, plaintext, nil), nil
}

func aeadDecrypt(key, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSize)
	return aead.Open(nil, nonce, ciphertext, nil)
}

func hasFlag(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
