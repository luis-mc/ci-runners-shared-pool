package mirror

import "sort"

func sortStates(s []MirrorState) {
	sort.Slice(s, func(i, j int) bool { return s[i].Repo < s[j].Repo })
}
