package idgen

// Generator produces a short code for a new URL.
type Generator interface {
	Generate() (string, error)
}

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func encodeBase62(n int64) string {
	if n == 0 {
		return string(base62Chars[0])
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{base62Chars[n%62]}, buf...)
		n /= 62
	}
	return string(buf)
}
