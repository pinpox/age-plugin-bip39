# age-plugin-bip39

An [age](https://age-encryption.org) plugin that derives encryption keys from
[BIP39](https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki) seed
phrases. This lets you use a memorable 24-word mnemonic as your age identity.

![demo](https://github.com/user-attachments/assets/8b628475-554a-4e6f-bac5-2ea398522533)

## Usage

The environment variable `AGE_PLUGIN_BIP39_CACHE` configures cache TTL for
derived keys (default: `10m`, `0` to disable). Uses the Linux kernel keyring to
store them in memory.

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


## How it works

The 24-word mnemonic encodes 256 bits of entropy. The plugin extracts this
entropy via BIP39, derives an X25519 keypair using the same method as
[melt](https://github.com/charmbracelet/melt) (SHA-512 of the entropy, clamped
to a Curve25519 scalar), and uses the standard age X25519 recipient mechanism
for encryption and decryption.
