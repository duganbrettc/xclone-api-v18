package main

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openapiJSON []byte

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(openapiJSON) //nolint
}
