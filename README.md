# age-plugin-bip39

An [age](https://age-encryption.org) plugin that derives encryption keys from
[BIP39](https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki) seed
phrases. This lets you use a memorable 24-word mnemonic as your age identity.

[![demo](https://asciinema.org/a/ch1qW7BykEYhYePc.svg)](https://asciinema.org/a/ch1qW7BykEYhYePc)

## Installation

```
go install github.com/pinpox/age-plugin-bip39@latest
```

The binary must be in your `$PATH` for age to discover it.

## Usage

### Generate an identity

```
age-plugin-bip39 -k > identity.txt
```

An interactive TUI lets you accept a generated phrase or enter your own. The
public key is printed to stderr, the identity to stdout.

For non-interactive use, pipe a mnemonic on stdin:

```
echo "abandon abandon ... art" | age-plugin-bip39 -k > identity.txt
```

### Encrypt

Use the public key shown during keygen:

```
age -r age1... -o secret.age secret.txt
```

### Decrypt

```
age -d -i identity.txt secret.age > secret.txt
```

age will invoke the plugin automatically and prompt for your seed phrase.

## Environment

| Variable | Description |
|---|---|
| `AGE_PLUGIN_BIP39_CACHE` | Cache TTL for derived keys (default: `10m`, `0` to disable). Uses the Linux kernel keyring. |

## How it works

The 24-word mnemonic encodes 256 bits of entropy. The plugin extracts this
entropy via BIP39, derives an X25519 keypair using the same method as
[melt](https://github.com/charmbracelet/melt) (SHA-512 of the entropy, clamped
to a Curve25519 scalar), and uses the standard age X25519 recipient mechanism
for encryption and decryption.

## License

MIT
