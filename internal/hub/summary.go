package hub

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Summarize renders a bus event as one line.
func Summarize(typ string, raw json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	str := func(k string) string {
		v, _ := m[k].(string)
		return v
	}
	switch typ {
	case "file_touch":
		s := fmt.Sprintf("%s %s", str("op"), filepath.Base(str("path")))
		if i := str("intent"); i != "" {
			s += ": " + i
		} else if sum := str("summary"); sum != "" {
			s += ": " + sum
		}
		return s
	case "notification":
		return oneLine(str("message"), 160)
	case "status_change":
		return fmt.Sprintf("%s → %s", str("old_status"), str("new_status"))
	case "member_change":
		return str("action")
	case "plan_update":
		return fmt.Sprintf("plan updated (%v items)", m["item_count"])
	case "plan_proposal":
		return fmt.Sprintf("plan proposed (%v items)", m["item_count"])
	case "context_update":
		return "context " + str("key")
	}
	return typ
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
