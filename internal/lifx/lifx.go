package lifx

import (
	"fmt"
	"log"
	"strings"

	"github.com/2tvenom/golifx"
)

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
