package manager

import (
	"encoding/hex"
	"fmt"
)

// These identifiers are not credentials. They bind local observations to one
// incarnation even when an imported or later instance reuses the same ID.
func validLocalTrafficID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == id
}

func validateLocalTrafficIDs(st State, ids map[string]string) error {
	if len(ids) != len(st.Instances) {
		return fmt.Errorf("identity count does not match instances")
	}
	seen := make(map[string]bool, len(ids))
	for _, instance := range st.Instances {
		id := ids[instance.ID]
		if !validLocalTrafficID(id) || seen[id] {
			return fmt.Errorf("missing, malformed, or duplicate identity")
		}
		seen[id] = true
	}
	return nil
}

func freshLocalTrafficIDs(st State) map[string]string {
	return nextLocalTrafficIDs(State{}, st, nil)
}

func nextLocalTrafficIDs(old, next State, previous map[string]string) map[string]string {
	ids := make(map[string]string, len(next.Instances))
	used := make(map[string]bool, len(previous)+len(next.Instances))
	for _, id := range previous {
		used[id] = true
	}
	for _, instance := range next.Instances {
		if prior, ok := old.Instance(instance.ID); ok && prior.Kind == instance.Kind && validLocalTrafficID(previous[instance.ID]) {
			ids[instance.ID] = previous[instance.ID]
			continue
		}
		for {
			id := randomHex(16)
			if !used[id] {
				ids[instance.ID], used[id] = id, true
				break
			}
		}
	}
	return ids
}
