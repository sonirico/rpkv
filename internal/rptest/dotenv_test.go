package rptest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDotenv(t *testing.T) {
	t.Run("missing file is not an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")

		err := loadDotenv(path)

		require.NoError(t, err)
	})

	t.Run("sets variables from the file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(
			t,
			os.WriteFile(path, []byte("RPKV_TEST_DOTENV_SET=value"), 0o600),
		)
		t.Cleanup(func() {
			require.NoError(t, os.Unsetenv("RPKV_TEST_DOTENV_SET"))
		})

		err := loadDotenv(path)

		require.NoError(t, err)
		assert.Equal(t, "value", os.Getenv("RPKV_TEST_DOTENV_SET"))
	})

	t.Run("does not override the real environment", func(t *testing.T) {
		t.Setenv("RPKV_TEST_DOTENV_KEEP", "from-env")
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(
			t,
			os.WriteFile(path, []byte("RPKV_TEST_DOTENV_KEEP=from-file"), 0o600),
		)

		err := loadDotenv(path)

		require.NoError(t, err)
		assert.Equal(t, "from-env", os.Getenv("RPKV_TEST_DOTENV_KEEP"))
	})

	t.Run("malformed file is an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".env")
		require.NoError(
			t,
			os.WriteFile(path, []byte(`KEY="unterminated`), 0o600),
		)

		err := loadDotenv(path)

		require.Error(t, err)
	})
}
