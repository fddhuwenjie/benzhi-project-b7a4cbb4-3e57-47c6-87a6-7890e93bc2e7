package httpapi

const (
	statusValidation    = 400
	statusConflict      = 409
	statusServerError   = 500
	statusClientClosed  = 499 // nginx-style Client Closed Request
)
