package coord

import (
	"net/http"
	"strings"

	"github.com/jufianto/sevjala/localapi"
)

const minPrefix = 4

// resolve picks one item by exact agent_id, else hostname (any case), else a
// unique agent_id prefix of at least minPrefix characters.
func resolve[T any](items []T, q string, key func(T) (id, host string), hint string) (T, error) {
	var zero T
	q = strings.TrimSpace(q)
	for _, it := range items {
		if id, _ := key(it); id == q {
			return it, nil
		}
	}
	var hits []T
	for _, it := range items {
		if _, host := key(it); strings.EqualFold(host, q) {
			hits = append(hits, it)
		}
	}
	if len(hits) == 0 && len(q) >= minPrefix {
		for _, it := range items {
			if id, _ := key(it); strings.HasPrefix(id, strings.ToLower(q)) {
				hits = append(hits, it)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return zero, localapi.Errorf(http.StatusNotFound, "no agent matches %q; %s", q, hint)
	}
	names := make([]string, len(hits))
	for i, it := range hits {
		id, host := key(it)
		names[i] = host + " (" + id + ")"
	}
	return zero, localapi.Errorf(http.StatusConflict, "%q matches %d agents: %s; use more of the agent id",
		q, len(hits), strings.Join(names, ", "))
}
