package display

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dre4success/tripartite/agent"
)

// PrintEvent renders a minimal human-readable event line.
func PrintEvent(e agent.Event) {
	prefix := fmt.Sprintf("[%s][%s]", strings.ToUpper(e.Agent), e.Type)
	if e.Data == nil {
		fmt.Println(prefix)
		return
	}

	switch v := e.Data.(type) {
	case string:
		if v == "" {
			fmt.Println(prefix)
			return
		}
		fmt.Printf("%s %s\n", prefix, v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			fmt.Printf("%s %v\n", prefix, v)
			return
		}
		fmt.Printf("%s %s\n", prefix, string(b))
	}
}
