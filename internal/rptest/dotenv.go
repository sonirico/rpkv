package rptest

import (
	"errors"
	"io/fs"

	"github.com/joho/godotenv"
)

// dotenvFile is loaded by Main from the test binary's working directory
// (go test runs each package's binary in its package dir), so every
// integration package can keep a local, gitignored .env for convenience.
const dotenvFile = ".env"

// loadDotenv loads path into the process environment. Variables already
// set in the real environment win over the file (godotenv.Load never
// overrides). A missing file is not an error.
func loadDotenv(path string) error {
	if err := godotenv.Load(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
