/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package schema

import (
	"github.com/pkg/errors"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

// A Request represents a GraphQL request.  It makes no guarantees that the
// request is valid.
type Request struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

// Operation finds the operation in req, if it is a valid request for GraphQL
// schema s. If the request is GraphQL valid, it must contain a single valid
// Operation.  If either the request is malformed or doesn't contain a valid
// operation, all GraphQL errors encountered are returned.
func (s *schema) Operation(req *Request) (Operation, error) {
	if req == nil || req.Query == "" {
		return nil, errors.New("no query string supplied in request")
	}

	doc, gqlErr := parser.ParseQuery(&ast.Source{Input: req.Query})
	if gqlErr != nil {
		return nil, gqlErr
	}

	listErr := validator.Validate(s.schema, doc)
	if len(listErr) != 0 {
		return nil, listErr
	}

	if len(doc.Operations) > 1 && req.OperationName == "" {
		return nil, errors.Errorf("Operation name must by supplied when query has more " +
			"than 1 operation.")
	}

	op := doc.Operations.ForName(req.OperationName)
	if op == nil {
		return nil, errors.Errorf("Supplied operation name %s isn't present in the request.",
			req.OperationName)
	}

	vars, gqlErr := validator.VariableValues(s.schema, op, req.Variables)
	if gqlErr != nil {
		return nil, gqlErr
	}

	operation := &operation{op: op,
		vars:     vars,
		query:    req.Query,
		doc:      doc,
		inSchema: s,
	}

	// recursively expand fragments in operation as selection set fields
	for _, s := range op.SelectionSet {
		recursivelyExpandFragmentSelections(s.(*ast.Field), operation)
	}

	return operation, nil
}

// recursivelyExpandFragmentSelections puts a fragment's selection set directly inside this
// field's selection set, and does it recursively for all the fields in this field's selection
// set. This eventually expands all the fragment references anywhere in the hierarchy.
func recursivelyExpandFragmentSelections(field *ast.Field, op *operation) {
	// find all valid type names that this field satisfies
	typeName := field.Definition.Type.Name()
	satisfies := []string{typeName}
	var additionalTypes []*ast.Definition
	switch op.inSchema.schema.Types[typeName].Kind {
	case ast.Interface:
		additionalTypes = op.inSchema.schema.PossibleTypes[typeName]
	case ast.Union:
		additionalTypes = op.inSchema.schema.PossibleTypes[typeName]
	case ast.Object:
		additionalTypes = op.inSchema.schema.Implements[typeName]
	default:
		// return, as fragment can't be present on a field which is not Interface, Union or Object
		return
	}
	for _, typ := range additionalTypes {
		satisfies = append(satisfies, typ.Name)
	}

	// collect all fields from any satisfying fragments into selectionSet
	collectedFields := collectFields(&requestContext{
		RawQuery:  op.query,
		Variables: op.vars,
		Doc:       op.doc,
	}, field.SelectionSet, satisfies)
	field.SelectionSet = make([]ast.Selection, 0, len(collectedFields))
	for _, collectedField := range collectedFields {
		field.SelectionSet = append(field.SelectionSet, collectedField.Field)
	}

	// recursively run for this field's selectionSet
	for _, f := range field.SelectionSet {
		recursivelyExpandFragmentSelections(f.(*ast.Field), op)
	}
}
