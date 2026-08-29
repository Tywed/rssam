package middleware

import (
	"encoding/json"
	"net/http"
)

type errorResponse struct {
	ErrorMessage string `json:"error_message"`
}

// WriteJSONError writes a JSON error body matching the API contract.
func WriteJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{ErrorMessage: msg})
}
