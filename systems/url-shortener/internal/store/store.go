package store

// Store persists the mapping from a short code to the original long URL.
// Save fails if the code is already taken, which is how idgen.Random's
// collision check is actually backed.
type Store interface {
	Save(code, longURL string) error
	Load(code string) (longURL string, ok bool)
	Exists(code string) bool
}
