// Copyright 2019 DeepMap, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package codegen

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/labstack/echo/v4"
)

var (
	contentTypesJSON   = []string{echo.MIMEApplicationJSON, "text/x-json"}
	contentTypesYAML   = []string{"application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"}
	contentTypesXML    = []string{echo.MIMEApplicationXML, echo.MIMETextXML}
	responseTypeSuffix = "Response"
)

// This function takes an array of Parameter definition, and generates a valid
// Go parameter declaration from them, eg:
// ", foo int, bar string, baz float32". The preceding comma is there to save
// a lot of work in the template engine.
func genParamArgs(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		paramName := p.GoVariableName()
		parts[i] = fmt.Sprintf("%s %s", paramName, p.TypeDef())
	}
	return ", " + strings.Join(parts, ", ")
}

// This function is much like the one above, except it only produces the
// types of the parameters for a type declaration. It would produce this
// from the same input as above:
// ", int, string, float32".
func genParamTypes(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.TypeDef()
	}
	return ", " + strings.Join(parts, ", ")
}

// This is another variation of the function above which generates only the
// parameter names:
// ", foo, bar, baz"
func genParamNames(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.GoVariableName()
	}
	return ", " + strings.Join(parts, ", ")
}

func genParamFmtString(path string) string {
	return ReplacePathParamsWithStr(path)
}

// genResponsePayload generates the payload returned at the end of each client request function
func genResponsePayload(operationID string) string {
	var buffer = bytes.NewBufferString("")

	// Here is where we build up a response:
	fmt.Fprintf(buffer, "&%s{\n", genResponseTypeName(operationID))
	fmt.Fprintf(buffer, "Body: bodyBytes,\n")
	fmt.Fprintf(buffer, "HTTPResponse: rsp,\n")
	fmt.Fprintf(buffer, "}")

	return buffer.String()
}

// genResponseUnmarshal generates unmarshaling steps for structured response payloads
func genResponseUnmarshal(op *OperationDefinition) string {
	var buffer = bytes.NewBufferString("")
	var mostSpecific = make(map[string]string)  // content-type and status-code
	var lessSpecific = make(map[string]string)  // status-code only
	var leastSpecific = make(map[string]string) // content-type only (default responses)

	// Get the type definitions from the operation:
	typeDefinitions, err := op.GetResponseTypeDefinitions()
	if err != nil {
		panic(err)
	}

	// Add a case for each possible response:
	responses := op.Spec.Responses
	for _, typeDefinition := range typeDefinitions {
		responseRef, ok := responses[typeDefinition.ResponseName]
		if !ok {
			continue
		}

		// We can't do much without a value:
		if responseRef.Value == nil {
			fmt.Fprintf(os.Stderr, "Response %s.%s has nil value\n", op.OperationId, typeDefinition.ResponseName)
			continue
		}

		// If there is no content-type then we have no unmarshaling to do:
		if len(responseRef.Value.Content) == 0 {
			caseAction := "break // No content-type"
			if typeDefinition.ResponseName == "default" {
				caseClause := "default:"
				leastSpecific[caseClause] = caseAction
			} else {
				caseClause := fmt.Sprintf("case rsp.StatusCode == %s:", typeDefinition.ResponseName)
				lessSpecific[caseClause] = caseAction
			}
			continue
		}

		// If we made it this far then we need to handle unmarshaling for each content-type:
		sortedContentKeys := SortedContentKeys(responseRef.Value.Content)
		for _, contentTypeName := range sortedContentKeys {

			// We get "interface{}" when using "anyOf" or "oneOf" (which doesn't work with Go types):
			if typeDefinition.TypeName == "interface{}" {
				// Unable to unmarshal this, so we leave it out:
				continue
			}

			// Add content-types here (json / yaml / xml etc):
			switch {

			// JSON:
			case StringInArray(contentTypeName, contentTypesJSON):
				caseAction := fmt.Sprintf("response.%s = &%s{} \n if err := json.Unmarshal(bodyBytes, response.%s); err != nil { \n return nil, err \n}", typeDefinition.TypeName, typeDefinition.Schema.TypeDecl(), typeDefinition.TypeName)
				if typeDefinition.ResponseName == "default" {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"json\"):", echo.HeaderContentType)
					leastSpecific[caseClause] = caseAction
				} else {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"json\") && rsp.StatusCode == %s:", echo.HeaderContentType, typeDefinition.ResponseName)
					mostSpecific[caseClause] = caseAction
				}

			// YAML:
			case StringInArray(contentTypeName, contentTypesYAML):
				caseAction := fmt.Sprintf("response.%s = &%s{} \n if err := yaml.Unmarshal(bodyBytes, response.%s); err != nil { \n return nil, err \n}", typeDefinition.TypeName, typeDefinition.Schema.TypeDecl(), typeDefinition.TypeName)
				if typeDefinition.ResponseName == "default" {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"yaml\"):", echo.HeaderContentType)
					leastSpecific[caseClause] = caseAction
				} else {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"yaml\") && rsp.StatusCode == %s:", echo.HeaderContentType, typeDefinition.ResponseName)
					mostSpecific[caseClause] = caseAction
				}

			// XML:
			case StringInArray(contentTypeName, contentTypesXML):
				caseAction := fmt.Sprintf("response.%s = &%s{} \n if err := xml.Unmarshal(bodyBytes, response.%s); err != nil { \n return nil, err \n}", typeDefinition.TypeName, typeDefinition.Schema.TypeDecl(), typeDefinition.TypeName)
				if typeDefinition.ResponseName == "default" {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"xml\"):", echo.HeaderContentType)
					leastSpecific[caseClause] = caseAction
				} else {
					caseClause := fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"xml\") && rsp.StatusCode == %s:", echo.HeaderContentType, typeDefinition.ResponseName)
					mostSpecific[caseClause] = caseAction
				}

			// Everything else:
			default:
				caseAction := fmt.Sprintf("// Content-type (%s) unsupported", contentTypeName)
				if typeDefinition.ResponseName == "default" {
					caseClause := "default:"
					leastSpecific[caseClause] = caseAction
				} else {
					caseClause := fmt.Sprintf("case rsp.StatusCode == %s:", typeDefinition.ResponseName)
					lessSpecific[caseClause] = caseAction
				}
			}
		}
	}

	// Now build the switch statement in order of most-to-least specific:
	fmt.Fprintf(buffer, "switch {\n")
	for _, caseClause := range SortedStringKeys(mostSpecific) {
		fmt.Fprintf(buffer, "%s\n%s\n", caseClause, mostSpecific[caseClause])
	}
	for _, caseClause := range SortedStringKeys(lessSpecific) {
		fmt.Fprintf(buffer, "%s\n%s\n", caseClause, lessSpecific[caseClause])
	}
	for _, caseClause := range SortedStringKeys(leastSpecific) {
		fmt.Fprintf(buffer, "%s\n%s\n", caseClause, leastSpecific[caseClause])
	}
	fmt.Fprintf(buffer, "}\n")

	return buffer.String()
}

// genResponseTypeName creates the name of generated response types (given the operationID):
func genResponseTypeName(operationID string) string {
	return fmt.Sprintf("%s%s", LowercaseFirstCharacter(operationID), responseTypeSuffix)
}

func getResponseTypeDefinitions(op *OperationDefinition) []TypeDefinition {
	td, err := op.GetResponseTypeDefinitions()
	if err != nil {
		panic(err)
	}
	return td
}

// This outputs a string array
func toStringArray(sarr []string) string {
	return `[]string{"` + strings.Join(sarr, `","`) + `"}`
}

// This function map is passed to the template engine, and we can call each
// function here by keyName from the template code.
var TemplateFunctions = template.FuncMap{
	"genParamArgs":               genParamArgs,
	"genParamTypes":              genParamTypes,
	"genParamNames":              genParamNames,
	"genParamFmtString":          genParamFmtString,
	"swaggerUriToEchoUri":        SwaggerUriToEchoUri,
	"swaggerUriToChiUri":         SwaggerUriToChiUri,
	"lcFirst":                    LowercaseFirstCharacter,
	"camelCase":                  ToCamelCase,
	"genResponsePayload":         genResponsePayload,
	"genResponseTypeName":        genResponseTypeName,
	"genResponseUnmarshal":       genResponseUnmarshal,
	"getResponseTypeDefinitions": getResponseTypeDefinitions,
	"toStringArray":              toStringArray,
	"lower":                      strings.ToLower,
	"title":                      strings.Title,
}
