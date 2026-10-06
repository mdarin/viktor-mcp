package scoring

import "fmt"

const (
	IntentMustClear = "Must clear"
	IntentQuickWins = "Quick wins"
	IntentFocus     = "Focus"
)

type Cluster struct {
	Intent string
	Items  []Scored
}

func ClusterIntents(ranked []Scored) []Cluster {
	must, quick, focus := 0, 0, 0
	for _, s := range ranked {
		if fires(s, "overdue") || fires(s, "door_closes") {
			must++
			continue
		}
		if fires(s, "quick_win") {
			quick++
			continue
		}
		focus++
	}
	out := []Cluster{
		{Intent: IntentMustClear, Items: make([]Scored, 0, must)},
		{Intent: IntentQuickWins, Items: make([]Scored, 0, quick)},
		{Intent: IntentFocus, Items: make([]Scored, 0, focus)},
	}
	for _, s := range ranked {
		switch {
		case fires(s, "overdue") || fires(s, "door_closes"):
			out[0].Items = append(out[0].Items, s)
		case fires(s, "quick_win"):
			out[1].Items = append(out[1].Items, s)
		default:
			out[2].Items = append(out[2].Items, s)
		}
	}
	nonEmpty := out[:0]
	for _, c := range out {
		if len(c.Items) > 0 {
			nonEmpty = append(nonEmpty, c)
		}
	}
	return nonEmpty
}

func fires(s Scored, name string) bool {
	for _, t := range s.Why {
		if t.Name == name {
			return true
		}
	}
	return false
}

func WhyTrace(s Scored) string {
	out := ""
	for i, t := range s.Why {
		if i > 0 {
			out += " · "
		}
		out += fmt.Sprintf("%s %.2f", t.Name, t.Value)
	}
	return out
}
