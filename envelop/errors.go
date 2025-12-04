package envelop


import "errors"

var (
	ErrInnerTooLarge   = errors.New("inner payload too large")
	ErrInvalidTTL      = errors.New("TTL must be > 0")
	ErrBadInnerLength  = errors.New("inner payload length mismatch")
)
