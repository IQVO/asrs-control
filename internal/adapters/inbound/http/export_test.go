package http

import "net/http"

// ProblemEntry exposes the production catalogue to the replay-class test.
type ProblemEntry struct {
	Slug   string
	Status int
	Class  string
}

// allProblems lists every problem type once (two errors share invalid-parameter).
func allProblems() []problem {
	out := []problem{idempotencyKeyRequired, idempotencyKeyReused, internalProblem, dependencyUnavailable}
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.slug] = true
	}
	for _, e := range problemCatalogue {
		if !seen[e.p.slug] {
			seen[e.p.slug] = true
			out = append(out, e.p)
		}
	}
	return out
}

func Catalogue() []ProblemEntry {
	var out []ProblemEntry
	for _, p := range allProblems() {
		out = append(out, ProblemEntry{Slug: p.slug, Status: p.status, Class: string(p.class)})
	}
	return out
}

// Refuse answers slug through the production writer, so the class marking runs.
func Refuse(w http.ResponseWriter, r *http.Request, slug string) {
	for _, p := range allProblems() {
		if p.slug == slug {
			writeProblem(w, r, p, "refused")
			return
		}
	}
	panic("unknown problem slug " + slug)
}
