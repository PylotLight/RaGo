package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	rag "rago/internal/rago"
)

func GetChatHandler(w http.ResponseWriter, r *http.Request) {
	// Extract input from the request (query parameters, JSON body, etc.)
	// Call your OpenAI model to generate a response
	// Write the response to the http.ResponseWriter
	prompt := r.PathValue("prompt")
	fmt.Fprintf(w, "Your GET chat response goes here\n")
	prompt = "scale syncthing deployment to 0"
	rag.LangChainQuery(prompt)
}

func PostChatHandler(w http.ResponseWriter, r *http.Request) {
	// Extract input from the request (query parameters, JSON body, etc.)
	// Call your OpenAI model to generate a response
	// Write the response to the http.ResponseWriter
	var jsonBody struct {
		Prompt string //`json:"Prompt"`
	}
	err := json.NewDecoder(r.Body).Decode(&jsonBody)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	result := rag.LangChainQuery(jsonBody.Prompt)

	// fmt.Fprintf(w, "Your POST chat response goes here\n%s", body)
	fmt.Fprint(w, result)

}
