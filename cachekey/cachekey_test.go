package cachekey

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKey(t *testing.T) {
	got := Key("tenant-A", "user:42")
	assert.Equal(t, "tenant:tenant-A:user:42", got)
}

func TestKey_DifferentTenantsProduceDifferentKeys(t *testing.T) {
	keyA := Key("tenant-A", "user:42")
	keyB := Key("tenant-B", "user:42")
	assert.NotEqual(t, keyA, keyB)
}