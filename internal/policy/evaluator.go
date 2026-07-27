package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

const SchemaVersion = "0.1.0"

var candidateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var codePriority = map[string]int{
	"INVALID_ACTION":              0,
	"UNKNOWN_OPERATION":           1,
	"OPERATION_NOT_ALLOWED":       2,
	"INVALID_TARGET":              3,
	"TARGET_OUTSIDE_ALLOWED_ROOT": 4,
	"READ_DENIED":                 5,
	"PROTECTED_PATH":              6,
	"MUTATION_LIMIT_EXCEEDED":     7,
	"MISSING_DEPENDENCY":          8,
}

var detailOrder = []string{"field", "operation", "dependency", "target", "current", "maximum"}

var mutatingOperations = map[string]struct{}{
	"filesystem.write":  {},
	"filesystem.delete": {},
	"service.deploy":    {},
}

type DAG struct {
	SchemaVersion string              `json:"schema_version"`
	Operations    map[string][]string `json:"operations"`
}

type Evaluator struct {
	operations map[string][]string
}

func Load(path string) (*Evaluator, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy DAG: %w", err)
	}
	var document DAG
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode policy DAG: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode policy DAG: invalid trailing content")
	}
	if document.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("policy DAG schema_version must be %s", SchemaVersion)
	}
	return New(document.Operations)
}

func New(operations map[string][]string) (*Evaluator, error) {
	normalized, err := validateDAG(operations)
	if err != nil {
		return nil, err
	}
	return &Evaluator{operations: normalized}, nil
}

func (e *Evaluator) Evaluate(
	ctx context.Context,
	candidates []action.Candidate,
	state benchmark.State,
	policy benchmark.Policy,
) ([]Result, error) {
	if e == nil {
		return nil, errors.New("policy evaluator is required")
	}
	if len(candidates) == 0 {
		return nil, errors.New("policy evaluator requires at least one action")
	}
	if err := validateInputs(state, policy); err != nil {
		return nil, err
	}
	results := make([]Result, len(candidates))
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		results[index] = e.evaluateOne(candidate, state, policy)
	}
	return results, nil
}

func (e *Evaluator) evaluateOne(
	candidate action.Candidate,
	state benchmark.State,
	policy benchmark.Policy,
) Result {
	actionID := candidate.CandidateID
	if !candidateIDPattern.MatchString(actionID) {
		actionID = "unknown"
	}
	malformed := validateCandidateShape(candidate)
	if len(malformed) > 0 {
		sortViolations(malformed)
		return buildResult(actionID, malformed)
	}

	violations := make([]Violation, 0)
	dependencies, knownOperation := e.operations[candidate.OperationClass]
	if !knownOperation {
		violations = append(violations, newViolation(
			"UNKNOWN_OPERATION",
			"operation", candidate.OperationClass,
		))
	} else if !contains(policy.AllowedOperationClasses, candidate.OperationClass) {
		violations = append(violations, newViolation(
			"OPERATION_NOT_ALLOWED",
			"operation", candidate.OperationClass,
		))
	}

	normalizedTarget, validTarget := normalizeVirtualPath(candidate.Target)
	if !validTarget {
		violations = append(violations, newViolation(
			"INVALID_TARGET",
			"target", candidate.Target,
		))
	} else {
		allowed := false
		for _, prefix := range policy.AllowedPathPrefixes {
			if pathWithin(normalizedTarget, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			violations = append(violations, newViolation(
				"TARGET_OUTSIDE_ALLOWED_ROOT",
				"target", normalizedTarget,
			))
		}
		if candidate.OperationClass == "filesystem.read" {
			for _, denied := range policy.DeniedReadPaths {
				if pathWithin(normalizedTarget, denied) {
					violations = append(violations, newViolation(
						"READ_DENIED",
						"target", normalizedTarget,
					))
					break
				}
			}
		} else {
			for _, protected := range policy.ProtectedPaths {
				if pathWithin(normalizedTarget, protected) {
					violations = append(violations, newViolation(
						"PROTECTED_PATH",
						"target", normalizedTarget,
					))
					break
				}
			}
		}
	}

	if knownOperation {
		if _, mutating := mutatingOperations[candidate.OperationClass]; mutating &&
			state.MutationCount >= policy.MaxMutations {
			violations = append(violations, newViolation(
				"MUTATION_LIMIT_EXCEEDED",
				"operation", candidate.OperationClass,
				"current", strconv.Itoa(state.MutationCount),
				"maximum", strconv.Itoa(policy.MaxMutations),
			))
		}
		completed := make(map[string]struct{}, len(state.CompletedOperations))
		for _, operation := range state.CompletedOperations {
			completed[operation] = struct{}{}
		}
		for _, dependency := range dependencies {
			if _, ok := completed[dependency]; !ok {
				violations = append(violations, newViolation(
					"MISSING_DEPENDENCY",
					"operation", candidate.OperationClass,
					"dependency", dependency,
				))
			}
		}
	}

	violations = deduplicateViolations(violations)
	sortViolations(violations)
	return buildResult(actionID, violations)
}

func validateDAG(source map[string][]string) (map[string][]string, error) {
	if source == nil {
		return nil, errors.New("policy DAG operations must be an object")
	}
	normalized := make(map[string][]string, len(source))
	for operation, dependencies := range source {
		if operation == "" {
			return nil, errors.New("policy DAG operation names must be non-empty")
		}
		values := append([]string(nil), dependencies...)
		seen := map[string]struct{}{}
		for _, dependency := range values {
			if dependency == "" {
				return nil, fmt.Errorf("policy DAG dependencies for %q must be non-empty", operation)
			}
			if _, exists := seen[dependency]; exists {
				return nil, fmt.Errorf("policy DAG dependencies for %q contain duplicates", operation)
			}
			seen[dependency] = struct{}{}
		}
		sort.Strings(values)
		normalized[operation] = values
	}
	for operation, dependencies := range normalized {
		for _, dependency := range dependencies {
			if _, exists := normalized[dependency]; !exists {
				return nil, fmt.Errorf(
					"policy DAG operation %q references unknown dependency %q",
					operation,
					dependency,
				)
			}
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(operation string) error {
		if visiting[operation] {
			return fmt.Errorf("policy DAG cycle detected at operation %q", operation)
		}
		if visited[operation] {
			return nil
		}
		visiting[operation] = true
		for _, dependency := range normalized[operation] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, operation)
		visited[operation] = true
		return nil
	}
	operations := make([]string, 0, len(normalized))
	for operation := range normalized {
		operations = append(operations, operation)
	}
	sort.Strings(operations)
	for _, operation := range operations {
		if err := visit(operation); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func validateInputs(state benchmark.State, policy benchmark.Policy) error {
	if state.MutationCount < 0 || policy.MaxMutations < 0 {
		return errors.New("state mutation_count and policy max_mutations must be non-negative")
	}
	if state.CompletedOperations == nil {
		return errors.New("state completed_operations must be an array")
	}
	if policy.AllowedOperationClasses == nil || policy.AllowedPathPrefixes == nil ||
		policy.ProtectedPaths == nil {
		return errors.New("policy operation, path, and protected-path fields must be arrays")
	}
	return nil
}

func validateCandidateShape(candidate action.Candidate) []Violation {
	violations := make([]Violation, 0)
	if !candidateIDPattern.MatchString(candidate.CandidateID) {
		violations = append(violations, newViolation("INVALID_ACTION", "field", "candidate_id"))
	}
	if strings.TrimSpace(candidate.OperationClass) == "" {
		violations = append(violations, newViolation("INVALID_ACTION", "field", "operation_class"))
	}
	if strings.TrimSpace(candidate.Tool) == "" || len(candidate.Tool) > 128 {
		violations = append(violations, newViolation("INVALID_ACTION", "field", "tool"))
	}
	if strings.TrimSpace(candidate.Target) == "" || len(candidate.Target) > 1024 {
		violations = append(violations, newViolation("INVALID_ACTION", "field", "target"))
	}
	if candidate.Arguments == nil {
		violations = append(violations, newViolation("INVALID_ACTION", "field", "arguments"))
	}
	seenDependencies := map[string]struct{}{}
	invalidDependencies := false
	for _, dependency := range candidate.DeclaredDependencies {
		if dependency == "" || len(dependency) > 128 {
			invalidDependencies = true
		}
		if _, exists := seenDependencies[dependency]; exists {
			invalidDependencies = true
		}
		seenDependencies[dependency] = struct{}{}
	}
	if candidate.DeclaredDependencies == nil || invalidDependencies {
		violations = append(violations, newViolation(
			"INVALID_ACTION",
			"field", "declared_dependencies",
		))
	}
	objectives := []struct {
		name  string
		value float64
		max   float64
	}{
		{name: "cost_units", value: candidate.EstimatedObjectives.CostUnits, max: math.Inf(1)},
		{name: "latency_ms", value: candidate.EstimatedObjectives.LatencyMS, max: math.Inf(1)},
		{name: "safety_risk", value: candidate.EstimatedObjectives.SafetyRisk, max: 1},
	}
	for _, objective := range objectives {
		if math.IsNaN(objective.value) || math.IsInf(objective.value, 0) ||
			objective.value < 0 || objective.value > objective.max {
			violations = append(violations, newViolation(
				"INVALID_ACTION",
				"field", "estimated_objectives."+objective.name,
			))
		}
	}
	return deduplicateViolations(violations)
}

func newViolation(code string, details ...string) Violation {
	values := make(map[string]string, len(details)/2)
	for index := 0; index+1 < len(details); index += 2 {
		values[details[index]] = details[index+1]
	}
	return Violation{Code: code, Details: values}
}

func deduplicateViolations(violations []Violation) []Violation {
	seen := map[string]struct{}{}
	result := make([]Violation, 0, len(violations))
	for _, violation := range violations {
		key := violation.Code + "\x00" + orderedDetails(violation.Details)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, violation)
	}
	return result
}

func sortViolations(violations []Violation) {
	sort.SliceStable(violations, func(left, right int) bool {
		leftPriority, leftKnown := codePriority[violations[left].Code]
		if !leftKnown {
			leftPriority = 999
		}
		rightPriority, rightKnown := codePriority[violations[right].Code]
		if !rightKnown {
			rightPriority = 999
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if violations[left].Code != violations[right].Code {
			return violations[left].Code < violations[right].Code
		}
		return orderedDetails(violations[left].Details) < orderedDetails(violations[right].Details)
	})
}

func orderedDetails(details map[string]string) string {
	var builder strings.Builder
	used := map[string]struct{}{}
	for _, key := range detailOrder {
		if value, exists := details[key]; exists {
			builder.WriteString(key)
			builder.WriteByte(0)
			builder.WriteString(value)
			builder.WriteByte(0)
			used[key] = struct{}{}
		}
	}
	extras := make([]string, 0)
	for key := range details {
		if _, exists := used[key]; !exists {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		builder.WriteString(key)
		builder.WriteByte(0)
		builder.WriteString(details[key])
		builder.WriteByte(0)
	}
	return builder.String()
}

func buildResult(actionID string, violations []Violation) Result {
	result := Result{
		ActionID:   actionID,
		Allowed:    len(violations) == 0,
		Violations: violations,
	}
	if result.Allowed {
		result.Projection = "<constraint_pass action_id=" + quoteAttribute(actionID) + "/>"
		return result
	}
	lines := make([]string, len(violations))
	for index, violation := range violations {
		attributes := []string{
			"action_id=" + quoteAttribute(actionID),
			"code=" + quoteAttribute(violation.Code),
		}
		used := map[string]struct{}{}
		for _, key := range detailOrder {
			if value, exists := violation.Details[key]; exists {
				attributes = append(attributes, key+"="+quoteAttribute(value))
				used[key] = struct{}{}
			}
		}
		extras := make([]string, 0)
		for key := range violation.Details {
			if _, exists := used[key]; !exists {
				extras = append(extras, key)
			}
		}
		sort.Strings(extras)
		for _, key := range extras {
			attributes = append(attributes, key+"="+quoteAttribute(violation.Details[key]))
		}
		lines[index] = "<constraint_violation " + strings.Join(attributes, " ") + "/>"
	}
	result.Projection = strings.Join(lines, "\n")
	return result
}

func quoteAttribute(value string) string {
	escaped := html.EscapeString(value)
	escaped = strings.ReplaceAll(escaped, "&#34;", `"`)
	escaped = strings.ReplaceAll(escaped, "&#39;", `'`)
	if !strings.Contains(value, `"`) {
		return `"` + escaped + `"`
	}
	if !strings.Contains(value, `'`) {
		return `'` + escaped + `'`
	}
	return `"` + strings.ReplaceAll(escaped, `"`, `&quot;`) + `"`
}

func normalizeVirtualPath(value string) (string, bool) {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return "", false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
}

func pathWithin(target, configured string) bool {
	configured = strings.TrimRight(configured, "/")
	prefix, valid := normalizeVirtualPath(configured)
	if !valid {
		return false
	}
	return target == prefix || strings.HasPrefix(target, prefix+"/")
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
