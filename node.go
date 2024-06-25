/*
Copyright 2023 eatmoreapple

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package juice

import (
	"fmt"
	"github.com/eatmoreapple/juice/eval"
	"reflect"
	"regexp"
	"strings"

	"github.com/eatmoreapple/juice/driver"
)

// paramRegex is a regular expression for parameter.
var paramRegex = regexp.MustCompile(`\#\{ *?([a-zA-Z0-9_\.]+) *?\}`)

// Node is a node of SQL.
type Node interface {
	// Accept accepts parameters and returns query and arguments.
	Accept(translator driver.Translator, p Parameter) (query string, args []any, err error)
}

// NodeGroup wraps multiple nodes.
type NodeGroup []Node

// Accept accepts parameters and returns query and arguments.
func (g NodeGroup) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	var builder = getBuilder()
	defer putBuilder(builder)
	for i, node := range g {
		q, a, err := node.Accept(translator, p)
		if err != nil {
			return "", nil, err
		}
		if len(q) > 0 {
			builder.WriteString(q)
		}
		if len(a) > 0 {
			args = append(args, a...)
		}
		if i < len(g)-1 && len(q) > 0 && !strings.HasSuffix(q, " ") {
			builder.WriteString(" ")
		}
	}
	return builder.String(), args, nil
}

// pureTextNode is a node of pure text.
var _ Node = (*pureTextNode)(nil)

// pureTextNode is a node of pure text.
// It is used to avoid unnecessary parameter replacement.
type pureTextNode string

func (p pureTextNode) Accept(_ driver.Translator, _ Parameter) (query string, args []any, err error) {
	return string(p), nil, nil
}

var _ Node = (*TextNode)(nil)

// TextNode is a node of text.
// What is the difference between TextNode and pureTextNode?
// TextNode is used to replace parameters with placeholders.
// pureTextNode is used to avoid unnecessary parameter replacement.
type TextNode struct {
	value            string
	placeholder      [][]string // for example, #{id}
	textSubstitution [][]string // for example, ${id}
}

// Accept accepts parameters and returns query and arguments.
// Accept implements Node interface.
func (c *TextNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	// If there is no parameter, return the value as it is.
	if len(c.placeholder) == 0 && len(c.textSubstitution) == 0 {
		return c.value, nil, nil
	}
	// Otherwise, replace the parameter with a placeholder.
	query, args, err = c.replaceHolder(c.value, args, translator, p)
	if err != nil {
		return "", nil, err
	}
	query, err = c.replaceTextSubstitution(query, p)
	if err != nil {
		return "", nil, err
	}
	return query, args, nil
}

func (c *TextNode) replaceHolder(query string, args []interface{}, translator driver.Translator, p Parameter) (string, []any, error) {
	for _, param := range c.placeholder {
		if len(param) != 2 {
			return "", nil, fmt.Errorf("invalid parameter %v", param)
		}
		matched, name := param[0], param[1]

		// try to get value from parameter
		value, exists := p.Get(name)
		if !exists {
			return "", nil, fmt.Errorf("parameter %s not found", name)
		}
		query = strings.Replace(query, matched, translator.Translate(name), 1)
		args = append(args, value.Interface())
	}
	return query, args, nil
}

// replaceTextSubstitution replaces text substitution.
func (c *TextNode) replaceTextSubstitution(query string, p Parameter) (string, error) {
	for _, sub := range c.textSubstitution {
		if len(sub) != 2 {
			return "", fmt.Errorf("invalid text substitution %v", sub)
		}
		matched, name := sub[0], sub[1]
		value, exists := p.Get(name)
		if !exists {
			return "", fmt.Errorf("parameter %s not found", name)
		}
		query = strings.Replace(query, matched, reflectValueToString(value), 1)
	}
	return query, nil
}

// build builds TextNode.
func (c *TextNode) build() {
	placeholder := paramRegex.FindAllStringSubmatch(c.value, -1)
	if len(placeholder) > 0 {
		c.placeholder = placeholder
	}
	textSubstitution := formatRegexp.FindAllStringSubmatch(c.value, -1)
	if len(textSubstitution) > 0 {
		c.textSubstitution = textSubstitution
	}
}

func NewTextNode(str string) Node {
	var node = &TextNode{value: str}
	node.build()
	return node
}

type ConditionNode struct {
	expr  eval.Expression
	Nodes NodeGroup
}

// Parse with given expression.
func (c *ConditionNode) Parse(test string) (err error) {
	c.expr, err = eval.Compile(test)
	return err
}

// Accept accepts parameters and returns query and arguments.
// Accept implements Node interface.
func (c *ConditionNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	matched, err := c.Match(p)
	if err != nil {
		return "", nil, err
	}
	if matched {
		return c.Nodes.Accept(translator, p)
	}
	return "", nil, nil
}

// Match returns true if test is matched.
func (c *ConditionNode) Match(p Parameter) (bool, error) {
	value, err := c.expr.Execute(p)
	if err != nil {
		return false, err
	}
	switch value.Kind() {
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() != 0, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint() != 0, nil
	case reflect.Float32, reflect.Float64:
		return value.Float() != 0, nil
	case reflect.String:
		return value.String() != "", nil
	default:
		return false, fmt.Errorf("unsupported type %s", value.Kind())
	}
}

var _ Node = (*IfNode)(nil)

// IfNode is a node of if.
type IfNode = ConditionNode

var _ Node = (*WhereNode)(nil)

// WhereNode is a node of where.
type WhereNode struct {
	Nodes []Node
}

// Accept accepts parameters and returns query and arguments.
func (w WhereNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	var builder = getBuilder()
	defer putBuilder(builder)
	for i, node := range w.Nodes {
		q, a, err := node.Accept(translator, p)
		if err != nil {
			return "", nil, err
		}
		if len(q) > 0 {
			builder.WriteString(q)
		}
		if len(a) > 0 {
			args = append(args, a...)
		}
		if i < len(w.Nodes)-1 && len(q) > 0 && !strings.HasSuffix(q, " ") {
			builder.WriteString(" ")
		}
	}
	query = builder.String()

	if query == "" {
		return
	}
	if strings.HasPrefix(query, "and") || strings.HasPrefix(query, "AND") {
		query = query[3:]
	} else if strings.HasPrefix(query, "or") || strings.HasPrefix(query, "OR") {
		query = query[2:]
	}
	query = strings.TrimSpace(query)

	if !(strings.HasPrefix(query, "where") || strings.HasPrefix(query, "WHERE")) {
		if !strings.HasPrefix(query, " ") {
			query = "WHERE " + query
		} else {
			query = "WHERE" + query
		}
	}
	return
}

var _ Node = (*TrimNode)(nil)

// TrimNode is a node of trim.
type TrimNode struct {
	Nodes           []Node
	Prefix          string
	PrefixOverrides []string
	Suffix          string
	SuffixOverrides []string
}

// Accept accepts parameters and returns query and arguments.
func (t TrimNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	var builder = getBuilder()
	defer putBuilder(builder)
	if t.Prefix != "" {
		builder.WriteString(t.Prefix)
	}
	for i, node := range t.Nodes {
		q, a, err := node.Accept(translator, p)
		if err != nil {
			return "", nil, err
		}
		if len(q) > 0 {
			builder.WriteString(q)
		}
		if !strings.HasSuffix(q, " ") && i < len(t.Nodes)-1 {
			builder.WriteString(" ")
		}
		if len(a) > 0 {
			args = append(args, a...)
		}
		if i < len(t.Nodes)-1 && len(q) > 0 && !strings.HasSuffix(q, " ") {
			builder.WriteString(" ")
		}
	}
	query = builder.String()
	if len(t.PrefixOverrides) > 0 {
		for _, prefix := range t.PrefixOverrides {
			if strings.HasPrefix(query, prefix) {
				query = strings.TrimPrefix(query, prefix)
				break
			}
		}
	}
	if len(t.SuffixOverrides) > 0 {
		for _, suffix := range t.SuffixOverrides {
			if strings.HasSuffix(query, suffix) {
				query = strings.TrimSuffix(query, suffix)
				break
			}
		}
	}
	if t.Suffix != "" {
		query += t.Suffix
	}
	return query, args, nil
}

var _ Node = (*ForeachNode)(nil)

// ForeachNode is a node of foreach.
type ForeachNode struct {
	Collection string
	Nodes      []Node
	Item       string
	Index      string
	Open       string
	Close      string
	Separator  string
}

// Accept accepts parameters and returns query and arguments.
func (f ForeachNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {

	// if item already exists
	if _, exists := p.Get(f.Item); exists {
		return "", nil, fmt.Errorf("item %s already exists", f.Item)
	}

	// one collection from parameter
	value, exists := p.Get(f.Collection)
	if !exists {
		return "", nil, fmt.Errorf("collection %s not found", f.Collection)
	}

	// if valueItem can not be iterated
	if !value.CanInterface() {
		return "", nil, fmt.Errorf("collection %s can not be iterated", f.Collection)
	}

	// if valueItem is not a slice
	for value.Kind() == reflect.Interface {
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Array, reflect.Slice:
		return f.acceptSlice(value, translator, p)
	case reflect.Map:
		return f.acceptMap(value, translator, p)
	default:
		return "", nil, fmt.Errorf("collection %s is not a slice or map", f.Collection)
	}
}

func (f ForeachNode) acceptSlice(value reflect.Value, translator driver.Translator, p Parameter) (query string, args []any, err error) {
	sliceLength := value.Len()

	if sliceLength == 0 {
		return "", nil, nil
	}

	var builder = getBuilder()
	defer putBuilder(builder)

	builder.WriteString(f.Open)

	end := sliceLength - 1

	// group wraps parameter
	// nil is for placeholder
	group := eval.ParamGroup{nil, p}

	for i := 0; i < sliceLength; i++ {

		item := value.Index(i).Interface()

		group[0] = eval.H{f.Item: item, f.Index: i}.AsParam()

		for _, node := range f.Nodes {
			q, a, err := node.Accept(translator, group)
			if err != nil {
				return "", nil, err
			}
			if len(q) > 0 {
				builder.WriteString(q)
			}
			if len(a) > 0 {
				args = append(args, a...)
			}
		}

		if i < end {
			builder.WriteString(f.Separator)
		}
	}

	// if sliceLength is not zero, add close
	builder.WriteString(f.Close)

	return builder.String(), args, nil
}

func (f ForeachNode) acceptMap(value reflect.Value, translator driver.Translator, p Parameter) (query string, args []any, err error) {
	keys := value.MapKeys()

	if len(keys) == 0 {
		return "", nil, nil
	}

	var builder = getBuilder()
	defer putBuilder(builder)

	builder.WriteString(f.Open)

	end := len(keys) - 1

	var index int

	// group wraps parameter
	// nil is for placeholder
	group := eval.ParamGroup{nil, p}

	for _, key := range keys {

		item := value.MapIndex(key).Interface()

		group[0] = eval.H{f.Item: item, f.Index: key.Interface()}.AsParam()

		for _, node := range f.Nodes {
			q, a, err := node.Accept(translator, group)
			if err != nil {
				return "", nil, err
			}
			if len(q) > 0 {
				builder.WriteString(q)
			}
			if len(a) > 0 {
				args = append(args, a...)
			}
		}

		if index < end {
			builder.WriteString(f.Separator)
		}

		index++
	}

	builder.WriteString(f.Close)

	return builder.String(), args, nil
}

// SetNode is a node of set.
type SetNode struct {
	Nodes []Node
}

// Accept accepts parameters and returns query and arguments.
func (s SetNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	var builder = getBuilder()
	defer putBuilder(builder)
	for i, node := range s.Nodes {
		q, a, err := node.Accept(translator, p)
		if err != nil {
			return "", nil, err
		}
		if len(q) > 0 {
			builder.WriteString(q)
		}
		if len(a) > 0 {
			args = append(args, a...)
		}
		if i < len(s.Nodes)-1 && len(q) > 0 && !strings.HasSuffix(q, " ") {
			builder.WriteString(" ")
		}
	}
	query = builder.String()
	if query != "" {
		query = "SET " + query
	}
	// trim space and comma
	for strings.HasSuffix(query, " ") {
		query = strings.TrimSuffix(query, " ")
	}
	query = strings.TrimSuffix(query, ",")
	return query, args, nil
}

// SQLNode is a node of sql.
// SQLNode defines a SQL query.
type SQLNode struct {
	id     string
	nodes  NodeGroup
	mapper *Mapper
}

// ID returns the id of the node.
func (s SQLNode) ID() string {
	return s.id
}

// Accept accepts parameters and returns query and arguments.
func (s SQLNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	return s.nodes.Accept(translator, p)
}

// IncludeNode is a node of include.
// It includes another SQL.
type IncludeNode struct {
	sqlNode Node
	mapper  *Mapper
	refId   string
}

// Accept accepts parameters and returns query and arguments.
func (i *IncludeNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	if i.sqlNode == nil {
		// lazy loading
		// does it need to be thread safe?
		sqlNode, err := i.mapper.GetSQLNodeByID(i.refId)
		if err != nil {
			return "", nil, err
		}
		i.sqlNode = sqlNode
	}
	return i.sqlNode.Accept(translator, p)
}

// ChooseNode is a node of choose.
// ChooseNode can have multiple when nodes and one otherwise node.
// WhenNode is executed when test is true.
// OtherwiseNode is executed when all when nodes are false.
type ChooseNode struct {
	WhenNodes     []Node
	OtherwiseNode Node
}

// Accept accepts parameters and returns query and arguments.
func (c ChooseNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	for _, node := range c.WhenNodes {
		q, a, err := node.Accept(translator, p)
		if err != nil {
			return "", nil, err
		}
		// if one of when nodes is true, return query and arguments
		if len(q) > 0 {
			return q, a, nil
		}
	}
	// if all when nodes are false, return otherwise node
	if c.OtherwiseNode != nil {
		return c.OtherwiseNode.Accept(translator, p)
	}
	return "", nil, nil
}

// WhenNode is a node of when.
// WhenNode like if node, but it can not be used alone.
// While one of WhenNode is true, the query of ChooseNode will be returned.
type WhenNode = ConditionNode

// OtherwiseNode is a node of otherwise.
// OtherwiseNode like else node, but it can not be used alone.
// If all WhenNode is false, the query of OtherwiseNode will be returned.
type OtherwiseNode struct {
	Nodes NodeGroup
}

// Accept accepts parameters and returns query and arguments.
func (o OtherwiseNode) Accept(translator driver.Translator, p Parameter) (query string, args []any, err error) {
	return o.Nodes.Accept(translator, p)
}

// valueItem is a element of ValuesNode.
type valueItem struct {
	column string
	value  string
}

// ValuesNode is a node of values.
// only support for insert.
type ValuesNode []*valueItem

// Accept accepts parameters and returns query and arguments.
func (v ValuesNode) Accept(translater driver.Translator, param Parameter) (query string, args []any, err error) {
	if len(v) == 0 {
		return "", nil, nil
	}
	builder := getBuilder()
	defer putBuilder(builder)
	builder.WriteString("(")
	builder.WriteString(v.columns())
	builder.WriteString(") VALUES (")
	builder.WriteString(v.values())
	builder.WriteString(")")
	node := NewTextNode(builder.String())
	return node.Accept(translater, param)
}

// columns returns columns of values.
func (v ValuesNode) columns() string {
	columns := make([]string, 0, len(v))
	for _, item := range v {
		columns = append(columns, item.column)
	}
	return strings.Join(columns, ", ")
}

// values returns values of values.
func (v ValuesNode) values() string {
	values := make([]string, 0, len(v))
	for _, item := range v {
		values = append(values, item.value)
	}
	return strings.Join(values, ", ")
}

// selectFieldAliasItem is a element of SelectFieldAliasNode.
type selectFieldAliasItem struct {
	column string
	alias  string
}

// SelectFieldAliasNode is a node of select field alias.
type SelectFieldAliasNode []*selectFieldAliasItem

// Accept accepts parameters and returns query and arguments.
func (s SelectFieldAliasNode) Accept(_ driver.Translator, _ Parameter) (query string, args []any, err error) {
	if len(s) == 0 {
		return "", nil, nil
	}
	fields := make([]string, 0, len(s))
	for _, item := range s {
		field := item.column
		if item.alias != "" && item.alias != item.column {
			field = field + " AS " + item.alias
		}
		fields = append(fields, field)
	}
	return strings.Join(fields, ", "), nil, nil
}

type primaryResult interface {
	Pk() *resultNode
}

// resultMapNode implements ResultMapper interface
type resultMapNode struct {
	id              string
	pk              *resultNode
	results         resultGroup
	associations    associationGroup
	collectionGroup collectionGroup
	binders         ResultBinder
}

func (r *resultMapNode) Pk() *resultNode {
	return r.pk
}

// ID returns id of resultMapNode.
func (r *resultMapNode) ID() string {
	return r.id
}

// init initializes resultMapNode
func (r *resultMapNode) init() error {
	var binderGroup ResultBinderGroup
	if r.pk != nil {
		binderGroup = append(binderGroup, fromResultNode(*r.pk))
	}
	if len(r.results) > 0 {
		binderGroup = append(binderGroup, fromResultNodeGroup(r.results)...)
	}
	if len(r.associations) > 0 {
		binderGroup = append(binderGroup, fromAssociationGroup(r.associations)...)
	}
	if len(r.collectionGroup) > 0 {
		binderGroup = append(binderGroup, fromCollectionGroup(r.collectionGroup)...)
	}
	r.binders = binderGroup
	return nil
}

// resultNode defines a resultNode mapping.
type resultNode struct {
	// property is the name of the property to map to.
	property string
	// column is the name of the column to map from.
	column string
}

// resultGroup defines a group of resultNode mappings.
type resultGroup []*resultNode

func (r resultGroup) Columns() (map[string]struct{}, error) {
	m := make(map[string]struct{})
	for _, v := range r {
		if _, ok := m[v.column]; ok {
			return nil, fmt.Errorf("field mapping %s is unbiguous", v.column)
		}
		m[v.column] = struct{}{}
	}
	return m, nil
}

// association is a collection of results and associations.
type association struct {
	property     string
	results      resultGroup
	associations associationGroup
}

func (a association) Columns() (map[string]struct{}, error) {
	columns := make(map[string]struct{})
	resultGroupColumns, err := a.results.Columns()
	if err != nil {
		return nil, err
	}
	for k, v := range resultGroupColumns {
		if _, ok := columns[k]; ok {
			return nil, fmt.Errorf("field mapping %s is unbiguous", k)
		}
		columns[k] = v
	}
	associationGroupColumns, err := a.associations.Columns()
	if err != nil {
		return nil, err
	}
	for k, v := range associationGroupColumns {
		if _, ok := columns[k]; ok {
			return nil, fmt.Errorf("field mapping %s is unbiguous", k)
		}
		columns[k] = v
	}
	return columns, nil
}

// associationGroup defines a group of association mappings.
type associationGroup []*association

func (a associationGroup) Columns() (map[string]struct{}, error) {
	m := make(map[string]struct{})
	for _, association := range a {
		columns, err := association.Columns()
		if err != nil {
			return nil, err
		}
		for k, v := range columns {
			if _, ok := m[k]; ok {
				return nil, fmt.Errorf("field mapping %s is unbiguous", k)
			}
			m[k] = v
		}
	}
	return m, nil
}

type collection struct {
	property         string
	resultGroup      resultGroup
	associationGroup associationGroup
}

func (c *collection) Columns() (map[string]struct{}, error) {
	m := make(map[string]struct{})
	resultGroupColumns, err := c.resultGroup.Columns()
	if err != nil {
		return nil, err
	}
	for k, v := range resultGroupColumns {
		if _, ok := m[k]; ok {
			return nil, fmt.Errorf("field mapping %s is unbiguous", k)
		}
		m[k] = v
	}
	associationGroupColumns, err := c.associationGroup.Columns()
	if err != nil {
		return nil, err
	}
	for k, v := range associationGroupColumns {
		if _, ok := m[k]; ok {
			return nil, fmt.Errorf("field mapping %s is unbiguous", k)
		}
		m[k] = v
	}
	return m, nil
}

type collectionGroup []*collection

func (c collectionGroup) Columns() (map[string]struct{}, error) {
	m := make(map[string]struct{})
	for _, collection := range c {
		columns, err := collection.Columns()
		if err != nil {
			return nil, err
		}
		for k, v := range columns {
			if _, ok := m[k]; ok {
				return nil, fmt.Errorf("field mapping %s is unbiguous", k)
			}
			m[k] = v
		}
	}
	return m, nil
}
