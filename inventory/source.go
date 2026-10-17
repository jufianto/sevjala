package inventory

import (
	"cmp"
	"slices"
)

// Source is a controller whose backups a node holds. Current is the one it is
// paired with; the others were replaced by a later pairing and never delete
// what they left.
type Source struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname,omitempty"`
	Backups  int    `json:"backups"`
	Current  bool   `json:"current,omitempty"`
}

// Sources groups ms by the controller that made them, the current one first.
// A source is named after its newest backup that carries a name.
func Sources(ms []Manifest, current string) []Source {
	var out []Source
	newest := map[string]Manifest{}
	for _, m := range ms {
		i := slices.IndexFunc(out, func(s Source) bool { return s.AgentID == m.SourceAgent })
		if i < 0 {
			out = append(out, Source{AgentID: m.SourceAgent, Current: m.SourceAgent == current})
			i = len(out) - 1
		}
		out[i].Backups++
		if n, ok := newest[m.SourceAgent]; m.SourceHost != "" && (!ok || m.CreatedAt.After(n.CreatedAt)) {
			newest[m.SourceAgent] = m
			out[i].Hostname = m.SourceHost
		}
	}
	slices.SortStableFunc(out, func(a, b Source) int {
		if a.Current != b.Current {
			if a.Current {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.Backups, a.Backups)
	})
	return out
}
