//go:build !linux

package main

func getCachedKey(name string) []byte {
	return nil
}

func cacheKey(name string, key []byte) {
}
