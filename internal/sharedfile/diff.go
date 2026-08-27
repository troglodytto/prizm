package sharedfile

import "sort"

// Change is one key whose value differs.
type Change struct {
	Key  string
	From string
	To   string
}

// Diff is a key-level comparison of two variable maps.
//
// Env files are not prose: diffing per key means editor key-reordering is not
// mistaken for a change, and it is why prizm never compares timestamps.
type Diff struct {
	Added   []string
	Removed []string
	Changed []Change
}

// Empty reports whether nothing differs.
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Compare diffs incoming against current. Results are sorted for stable output.
func Compare(current, incoming map[string]string) Diff {
	var d Diff

	for key, value := range incoming {
		old, ok := current[key]
		switch {
		case !ok:
			d.Added = append(d.Added, key)
		case old != value:
			d.Changed = append(d.Changed, Change{Key: key, From: old, To: value})
		}
	}
	for key := range current {
		if _, ok := incoming[key]; !ok {
			d.Removed = append(d.Removed, key)
		}
	}

	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Key < d.Changed[j].Key })
	return d
}
