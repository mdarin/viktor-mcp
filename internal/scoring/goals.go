package scoring

import (
	"regexp"
	"strconv"
	"strings"
)

// Goal axes (card #854): continuous goal parameters carried by the task
// description marker <!-- viktor:goals axis=value ... -->. Every axis is
// clamped to 0..1; 0.5 is neutral and contributes zero, so cards without
// the marker keep the legacy ranking exactly (no default-weight changes).
const (
	GoalSpeed   = "speed"
	GoalQuality = "quality"
	GoalSafety  = "safety"
	GoalScope   = "scope"
)

// goalAxisWeights are fixed coefficients, deliberately not user-tunable
// yet: the contribution only fires when the marker is present, and a
// single-user setup has no data to calibrate tunable axis weights.
var goalAxisWeights = map[string]float64{
	GoalSpeed:   3,
	GoalQuality: 4,
	GoalSafety:  3,
	// A broad scope argues against starting the task today, hence the
	// negative weight: scope=1 pushes the score down, scope=0 is "as
	// narrow as it gets" and gets the small bonus instead.
	GoalScope: -2,
}

var goalsMarkerRe = regexp.MustCompile(`<!-- viktor:goals ([^>]*?) -->`)

type Goals struct {
	Speed   float64
	Quality float64
	Safety  float64
	Scope   float64
}

// ParseGoals extracts the goals marker from a task description. Returns
// nil when no marker is present. Unknown axes and unparseable values are
// collected/ignored — a malformed marker never fails ranking.
func ParseGoals(desc string) (*Goals, []string) {
	m := goalsMarkerRe.FindStringSubmatch(desc)
	if m == nil {
		return nil, nil
	}
	g := Goals{Speed: 0.5, Quality: 0.5, Safety: 0.5, Scope: 0.5}
	var unknown []string
	for _, pair := range strings.Fields(m[1]) {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		switch k {
		case GoalSpeed, GoalQuality, GoalSafety, GoalScope:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				// Unparseable value on a known axis: keep the neutral default.
				continue
			}
			switch k {
			case GoalSpeed:
				g.Speed = clamp01(f)
			case GoalQuality:
				g.Quality = clamp01(f)
			case GoalSafety:
				g.Safety = clamp01(f)
			case GoalScope:
				g.Scope = clamp01(f)
			}
		default:
			unknown = append(unknown, k)
		}
	}
	return &g, unknown
}

// Contribution returns the deterministic score contribution of the goal
// vector: (value - 0.5) * 2 per axis, weighted by goalAxisWeights, so the
// neutral 0.5 adds nothing and the extremes span the full axis weight.
func (g Goals) Contribution() float64 {
	sum := 0.0
	for axis, weight := range goalAxisWeights {
		sum += (g.axisValue(axis) - 0.5) * 2 * weight
	}
	return sum
}

// Damped returns a copy with one axis scaled by factor (0..1). This is
// the hook future context modulation plugs into (e.g. repowise hotspot
// data damping the speed axis); it stays deterministic and traceable.
func (g Goals) Damped(axis string, factor float64) Goals {
	if factor < 0 {
		factor = 0
	}
	if factor > 1 {
		factor = 1
	}
	switch axis {
	case GoalSpeed:
		g.Speed = 0.5 + (g.Speed-0.5)*factor
	case GoalQuality:
		g.Quality = 0.5 + (g.Quality-0.5)*factor
	case GoalSafety:
		g.Safety = 0.5 + (g.Safety-0.5)*factor
	case GoalScope:
		g.Scope = 0.5 + (g.Scope-0.5)*factor
	}
	return g
}

func (g Goals) axisValue(axis string) float64 {
	switch axis {
	case GoalSpeed:
		return g.Speed
	case GoalQuality:
		return g.Quality
	case GoalSafety:
		return g.Safety
	case GoalScope:
		return g.Scope
	}
	return 0.5
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
