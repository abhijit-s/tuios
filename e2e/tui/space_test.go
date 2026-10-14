package tuie2e

import (
	"encoding/json"
	"testing"
)

// spaceList is the part of the list-spaces result these tests read.
type spaceList struct {
	Type   string `json:"type"`
	Spaces []struct {
		Name     string `json:"name"`
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	} `json:"spaces"`
}

// listSpaces calls list-spaces and returns each space with its sessions'
// names, failing the test on a reply that is not the daemon's.
func listSpaces(t *testing.T, base string) map[string][]string {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "list-spaces")
	if err != nil {
		t.Fatalf("list-spaces: %v: %s", err, out)
	}
	var list spaceList
	if err := json.Unmarshal([]byte(out), &list); err != nil || list.Type != "space_list" {
		t.Fatalf("ASSERTION: list-spaces did not answer with a space_list: %v: %s", err, out)
	}
	got := map[string][]string{}
	for _, sp := range list.Spaces {
		for _, s := range sp.Sessions {
			got[sp.Name] = append(got[sp.Name], s.Name)
		}
	}
	return got
}
