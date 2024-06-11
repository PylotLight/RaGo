package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	rag "rago/internal/rago"

	"github.com/sashabaranov/go-openai"
)

func HandleCompletionRequest(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("Got request for %s from %s\n", r.URL, r.RemoteAddr)
	var req openai.ChatCompletionRequest
	body, err := io.ReadAll(r.Body)
	apiKey := strings.Split(r.Header.Get("Authorization"), " ")[1]
	if err != nil {
		println(err.Error())
	}

	if err = json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request payload: %v", err), http.StatusBadRequest)
		return
	}

	stream, err := rag.GenerateCompletion(&req, apiKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating completion: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	if _, err := io.Copy(w, stream); err != nil {
		http.Error(w, fmt.Sprintf("Error streaming response: %v", err), http.StatusInternalServerError)
		return
	}

}
func GetModelHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("Got request for %s from %s\n", r.URL, r.RemoteAddr)
	apiKey := r.Header.Get("Authorization")
	if apiKey == "" {
		http.Error(w, "API key not set", http.StatusInternalServerError)
		return
	}

	var model openai.ModelsList
	model.Models = append(model.Models, openai.Model{ID: "llama3-70b-8192", Object: "llama3-70B", CreatedAt: time.Now().Unix(), OwnedBy: "Light"})
	// Create a response object based on the API spec

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model)
}
