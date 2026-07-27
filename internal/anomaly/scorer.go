package anomaly

import (
	"errors"
	"math"

	"bouncer/internal/monitoring"
)

// Score evaluates trusted monitoring features using the frozen forest.
func (r *Runtime) Score(features monitoring.Features) (Evaluation, error) {
	if r == nil {
		return Evaluation{}, errors.New("anomaly runtime is nil")
	}
	values, err := featureVector(features)
	if err != nil {
		return Evaluation{}, err
	}
	totalPath := 0.0
	for index := range r.artifact.Trees {
		totalPath += pathLength(&r.artifact.Trees[index], values)
	}
	averagePath := totalPath / float64(len(r.artifact.Trees))
	normalizer := averagePathLength(r.artifact.SampleSize)
	score := 0.0
	if normalizer > 0 {
		score = math.Pow(2, -averagePath/normalizer)
	}
	return Evaluation{
		Score:     score,
		Threshold: r.artifact.Threshold,
		Alert:     score >= r.artifact.Threshold,
	}, nil
}

func featureVector(features monitoring.Features) ([]float64, error) {
	values := []float64{
		features.RejectionRate,
		features.RetryRate,
		float64(features.NoProgressStreak),
		features.ToolSwitchRate,
		features.LatencyDeltaMS,
		features.TransitionNLL,
	}
	for _, value := range values {
		if !finite(value) {
			return nil, errors.New("anomaly features must be finite")
		}
	}
	if !probability(features.RejectionRate) || !probability(features.RetryRate) ||
		features.NoProgressStreak < 0 || !probability(features.ToolSwitchRate) ||
		features.TransitionNLL < 0 {
		return nil, errors.New("anomaly features are outside their frozen bounds")
	}
	return values, nil
}

func pathLength(root *Node, values []float64) float64 {
	node := root
	depth := 0
	for node.Feature != nil {
		if values[*node.Feature] < *node.Split {
			node = node.Left
		} else {
			node = node.Right
		}
		depth++
	}
	return float64(depth) + averagePathLength(node.Size)
}

func averagePathLength(size int) float64 {
	if size <= 1 {
		return 0
	}
	if size == 2 {
		return 1
	}
	return 2*(math.Log(float64(size-1))+0.5772156649) - 2*float64(size-1)/float64(size)
}
