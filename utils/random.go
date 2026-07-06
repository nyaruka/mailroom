package utils

import "github.com/nyaruka/gocommon/random"

var base64Chars = []rune(`ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/`)

// RandomBase64 returns a random string of length n composed of base64 characters. It draws from gocommon/random's
// current generator so tests can seed it.
func RandomBase64(n int) string {
	return random.String(n, base64Chars)
}
