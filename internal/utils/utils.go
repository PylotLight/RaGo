package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"

	"github.com/2tvenom/golifx"
	"github.com/sashabaranov/go-openai"
)

// Execute server commands
func ExecuteCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func GetLight(name string) *golifx.Bulb {
	bulbs, err := golifx.LookupBulbs()
	if err != nil {
		log.Fatalf("Error looking up bulbs: %v", err)
		return nil
	}

	bulbcount := len(bulbs)
	if bulbcount == 0 {
		log.Fatalf("%d bulbs found", bulbcount)
		return nil
	}

	for _, bulb := range bulbs {
		group, err := bulb.GetGroup()
		if err != nil {
			log.Printf("Error getting group for bulb %s: %v", group.Label, err)
			continue
		}
		if strings.EqualFold(group.Label, name) {
			return bulb
		}
	}
	return nil
}

func UpdateLight(light string, state bool) string {
	bulb := GetLight(light)
	group, _ := bulb.GetGroup()
	if bulb != nil {
		bulb.SetPowerState(state)
		return fmt.Sprintf("%s light has been set to %t", group.Label, state)
	}
	return fmt.Sprintf("Unable to find %s", group)
}

func WriteResponse(content string, pw *io.PipeWriter, req *openai.ChatCompletionRequest, resp *openai.ChatCompletionStreamResponse) {

	formattedResponse := openai.ChatCompletionStreamResponse{
		ID:                resp.ID,
		Object:            "chat.completion.chunk",
		Created:           resp.Created,
		Model:             req.Model,
		Choices:           []openai.ChatCompletionStreamChoice{{Index: 0, Delta: openai.ChatCompletionStreamChoiceDelta{Content: content}}},
		SystemFingerprint: resp.SystemFingerprint,
	}
	jsonResponse, err := json.Marshal(formattedResponse)
	if err != nil {
		pw.CloseWithError(err)
		return
	}
	prefixedResponse := fmt.Sprintf("data: %s\n", jsonResponse)
	if _, err := pw.Write([]byte(prefixedResponse)); err != nil {
		pw.CloseWithError(err)
		return
	}
}
