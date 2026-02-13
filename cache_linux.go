//go:build linux

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func cacheTTL() time.Duration {
	v := os.Getenv("AGE_PLUGIN_BIP39_CACHE")
	if v == "" {
		return 10 * time.Minute
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 10 * time.Minute
	}
	return d
}

func getCachedKey(name string) []byte {
	if cacheTTL() == 0 {
		return nil
	}

	id, err := unix.KeyctlSearch(unix.KEY_SPEC_USER_KEYRING, "user", name, 0)
	if err != nil {
		return nil
	}

	buf := make([]byte, 256)
	n, err := unix.KeyctlBuffer(unix.KEYCTL_READ, id, buf, 0)
	if err != nil || n <= 0 {
		return nil
	}

	key, err := hex.DecodeString(string(buf[:n]))
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}

func cacheKey(name string, key []byte) {
	ttl := cacheTTL()
	if ttl == 0 {
		return
	}

	payload := fmt.Sprintf("%x", key)
	id, err := unix.AddKey("user", name, []byte(payload), unix.KEY_SPEC_USER_KEYRING)
	if err != nil {
		return
	}

	// Set the key timeout
	_, _ = unix.KeyctlInt(unix.KEYCTL_SET_TIMEOUT, id, int(ttl.Seconds()), 0, 0)
}
