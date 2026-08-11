//go:build integration

package app_test

import (
	"testing"

	"github.com/sonirico/rpkv/internal/rptest"
)

func TestMain(m *testing.M) {
	rptest.Main(m)
}
