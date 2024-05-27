package main

import (
	"fmt"
	"net/http"
	v1 "rago/api/v1"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", v1.HandleCompletionRequest)
	fmt.Println("Server is listening on port 8080...")
	http.ListenAndServe(":8080", mux)
}
