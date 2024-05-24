package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	rag "rago/internal/rago"

	"github.com/sashabaranov/go-openai"
)

func HandleCompletionRequest(w http.ResponseWriter, r *http.Request) {
	var req openai.ChatCompletionRequest
	body, err := io.ReadAll(r.Body)
	io.Copy(os.Stdout, r.Body)
	if err != nil {
		println(err.Error())
	}

	if err = json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request payload: %v", err), http.StatusBadRequest)
		return
	}

	stream, err := rag.GenerateCompletion(req)
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
