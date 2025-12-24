package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

// ErrPayloadTooLarge is returned when the request body exceeds the size limit
var ErrPayloadTooLarge = errors.New("payload too large")

// ReadBodyStrict reads the request body and validates it's not empty.
// Enforces a size limit to prevent memory exhaustion attacks (DoS protection).
func ReadBodyStrict(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	// Limit payload to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	body, err := io.ReadAll(r.Body)
	defer func() {
		if closeErr := r.Body.Close(); closeErr != nil {
			log.Printf("[WEBHOOK] body_close=failed error=%v", closeErr)
		}
	}()
	if err != nil {
		// Check if error is due to body size limit
		if err.Error() == "http: request body too large" {
			return nil, fmt.Errorf("%w (max %d bytes)", ErrPayloadTooLarge, limit)
		}
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	return body, nil
}

// WriteJSON writes a JSON response with proper headers
func WriteJSON(w http.ResponseWriter, code int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	return json.NewEncoder(w).Encode(data)
}
