package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// decodeKey extracts the lookup key from the request's path value, decoding
// it per the key_encoding query parameter. No parameter means the path
// segment is the raw key (ServeMux already percent-decodes it); "base64url"
// decodes it as RFC 4648 base64url, padded or unpadded.
func decodeKey(r *http.Request) ([]byte, error) {
	enc := r.URL.Query().Get("key_encoding")
	switch enc {
	case "":
		return []byte(r.PathValue("key")), nil
	case "base64url":
		return base64.RawURLEncoding.DecodeString(strings.TrimRight(r.PathValue("key"), "="))
	default:
		return nil, fmt.Errorf("unsupported key_encoding %q", enc)
	}
}
