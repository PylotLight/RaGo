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

// Get non function calling curl request
// use rag/langchain to create new internal request with function calling
// use that function call request to create command
// run command and get output
// summerise and return to user
