package api

import (
	"fmt"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
)

// maxGraphQLComplexity caps the number of field selections a request may
// execute after named fragment spreads have been expanded. Every alias is a
// distinct field selection, so this also bounds the fan-out that a purely
// textual brace count cannot observe.
const maxGraphQLComplexity = 200

// queryCost describes the work a parsed GraphQL document will ask the executor
// to perform for the selected operation.
type queryCost struct {
	depth      int
	complexity int
}

// graphQLCostAnalyzer walks a parsed document and expands fragment spreads in
// place. It counts field selections (depth and complexity) the way the
// executor will see them rather than the way they appear in the request text.
type graphQLCostAnalyzer struct {
	fragments     map[string]*ast.FragmentDefinition
	visiting      map[string]bool
	depth         int
	complexity    int
	maxDepth      int
	maxComplexity int
	// limit bounds the walk itself (fragment spreads can expand
	// exponentially). Once complexity passes it the exact count no longer
	// matters, because the request is already over maxGraphQLComplexity.
	limit int
}

// analyzeGraphQLQuery parses query and reports the effective depth and field
// complexity of the operation the executor will run. When operationName is set,
// only that operation is considered; otherwise the most expensive operation in
// the document wins. Named fragment spreads are followed recursively (cycles
// are broken rather than recursed into) and aliases are counted individually.
// A parse error is returned to the caller so it can decide how to handle
// malformed input; graphql-go cannot execute a document that fails to parse.
func analyzeGraphQLQuery(query, operationName string) (queryCost, error) {
	doc, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		return queryCost{}, fmt.Errorf("parse GraphQL query: %w", err)
	}

	fragments := make(map[string]*ast.FragmentDefinition)
	for _, def := range doc.Definitions {
		if frag, ok := def.(*ast.FragmentDefinition); ok && frag.Name != nil {
			fragments[frag.Name.Value] = frag
		}
	}

	analyzer := &graphQLCostAnalyzer{
		fragments: fragments,
		limit:     maxGraphQLComplexity,
	}

	for _, def := range doc.Definitions {
		op, ok := def.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		if operationName != "" && (op.Name == nil || op.Name.Value != operationName) {
			continue
		}

		analyzer.visiting = make(map[string]bool)
		analyzer.depth = 0
		analyzer.complexity = 0
		analyzer.visitSelectionSet(op.SelectionSet, 1)

		if analyzer.depth > analyzer.maxDepth {
			analyzer.maxDepth = analyzer.depth
		}
		if analyzer.complexity > analyzer.maxComplexity {
			analyzer.maxComplexity = analyzer.complexity
		}
	}

	return queryCost{depth: analyzer.maxDepth, complexity: analyzer.maxComplexity}, nil
}

// visitSelectionSet accumulates the cost of a selection set. fieldDepth is the
// nesting level that fields directly inside set execute at. Fragments are
// transparent: they do not add a field level, but their fields are counted
// every time the fragment is spread.
func (a *graphQLCostAnalyzer) visitSelectionSet(set *ast.SelectionSet, fieldDepth int) {
	if set == nil {
		return
	}

	for _, sel := range set.Selections {
		if a.complexity > a.limit {
			return
		}

		switch node := sel.(type) {
		case *ast.Field:
			a.complexity++
			if fieldDepth > a.depth {
				a.depth = fieldDepth
			}
			a.visitSelectionSet(node.SelectionSet, fieldDepth+1)
		case *ast.InlineFragment:
			a.visitSelectionSet(node.SelectionSet, fieldDepth)
		case *ast.FragmentSpread:
			if node.Name == nil || a.visiting[node.Name.Value] {
				continue // break fragment cycles
			}
			frag, ok := a.fragments[node.Name.Value]
			if !ok {
				continue // undefined spread: the executor reports it
			}
			a.visiting[node.Name.Value] = true
			a.visitSelectionSet(frag.SelectionSet, fieldDepth)
			delete(a.visiting, node.Name.Value)
		}
	}
}
