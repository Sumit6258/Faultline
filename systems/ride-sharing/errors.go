package ridesharing

import "errors"

// Each of these means something different to a caller, and the HTTP layer maps them to different statuses.
var (
	ErrInvalidRequest      = errors.New("ridesharing: invalid request")
	ErrTripNotFound        = errors.New("ridesharing: trip not found")
	ErrDriverNotFound      = errors.New("ridesharing: driver not found")
	ErrQuoteNotFound       = errors.New("ridesharing: quote not found")
	ErrQuoteExpired        = errors.New("ridesharing: quote has expired")
	ErrQuoteUsed           = errors.New("ridesharing: quote was already used for another trip")
	ErrQuoteMismatch       = errors.New("ridesharing: quote is for a different trip")
	ErrIdempotencyConflict = errors.New("ridesharing: idempotency key was used with a different request")
	ErrRiderBusy           = errors.New("ridesharing: rider already has a trip in progress")
	ErrOfferExpired        = errors.New("ridesharing: offer has expired")
	ErrDriverBusy          = errors.New("ridesharing: driver is on a trip")
	ErrConflict            = errors.New("ridesharing: not allowed in the trip's current state")
)
