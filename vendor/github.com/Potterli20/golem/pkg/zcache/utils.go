package zcache

import "fmt"

const KeySplitter = "/"

func getKeyWithPrefix(prefix, key string) string {
	if prefix != "" {
		return fmt.Sprintf("%s%s%s", prefix, KeySplitter, key)
	}
	return key
}

func getKeysWithPrefix(prefix string, keys []string) []string {
	if prefix != "" {
		var newKeys []string
		for _, key := range keys {
			newKeys = append(newKeys, fmt.Sprintf("%s%s%s", prefix, KeySplitter, key))
		}
		return newKeys
	}

	return keys
}
