package juice

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

// ConfigurationParser is the interface for parsing configuration.
type ConfigurationParser interface {
	// Parse parses the configuration from the reader.
	Parse(reader io.Reader) (*Configuration, error)
}

// XMLParser is the parser for XML configuration.
type XMLParser struct {
	configuration Configuration
	FS            fs.FS
}

// Parse implements ConfigurationParser.
func (p XMLParser) Parse(reader io.Reader) (*Configuration, error) {
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "environments":
				envs, err := p.parseEnvironments(decoder, token)
				if err != nil {
					return nil, err
				}
				p.configuration.Environments = *envs
			case "mappers":
				var mappers = Mappers{cfg: &p.configuration}
				if err := p.parseMappers(&mappers, decoder); err != nil {
					return nil, err
				}
				p.configuration.Mappers = mappers
			case "settings":
				settings, err := p.parseSettings(decoder)
				if err != nil {
					return nil, err
				}
				p.configuration.Settings = *settings
			}
		}
	}
	return &p.configuration, nil
}

func (p XMLParser) parseEnvironments(decoder *xml.Decoder, token xml.StartElement) (*Environments, error) {
	var envs Environments
	for _, attr := range token.Attr {
		if attr.Name.Local == "default" {
			envs.Default = attr.Value
			break
		}
	}
	if envs.Default == "" {
		return nil, errors.New("default environment is required")
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "environment" {
				environment, err := p.parseEnvironment(decoder, token)
				if err != nil {
					return nil, err
				}
				if _, exists := envs.envs[environment.ID]; exists {
					return nil, fmt.Errorf("duplicate environment id: %s", environment.ID)
				}
				if envs.envs == nil {
					envs.envs = make(map[string]*Environment)
				}
				envs.envs[environment.ID] = environment
			}
		case xml.EndElement:
			if token.Name.Local == "environments" {
				return &envs, nil
			}
		}
	}
	return &envs, nil
}

func (p XMLParser) parseEnvironment(decoder *xml.Decoder, token xml.StartElement) (*Environment, error) {
	var env Environment
	for _, attr := range token.Attr {
		if attr.Name.Local == "id" {
			env.ID = attr.Value
			break
		}
	}
	if env.ID == "" {
		return nil, errors.New("environment id is required")
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "dataSource":
				env.DataSource, err = p.parseDataSource(decoder)
				if err != nil {
					return nil, err
				}
			case "driver":
				env.Driver, err = p.parseDriver(decoder)
				if err != nil {
					return nil, err
				}
			case "maxIdleConnNum":
				env.MaxIdleConnNum, err = p.parseMaxIdleConnNum(decoder)
				if err != nil {
					return nil, err
				}
			case "maxOpenConnNum":
				env.MaxOpenConnNum, err = p.parseMaxOpenConnNum(decoder)
				if err != nil {
					return nil, err
				}
			case "maxLifetime":
				env.MaxConnLifetime, err = p.parseMaxConnLifetime(decoder)
				if err != nil {
					return nil, err
				}
			case "maxIdleConnLifetime":
				env.MaxIdleConnLifetime, err = p.parseMaxIdleConnLifetime(decoder)
				if err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if token.Name.Local == "environment" {
				return &env, nil
			}
		}
	}
	return &env, nil
}

func (p XMLParser) parseMappers(mappers *Mappers, decoder *xml.Decoder) error {
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "mapper" {
				mapper, err := p.parseMapper(decoder, token)
				if err != nil {
					return err
				}
				for key, stmt := range mapper.statements {
					if err = mappers.setStatementByID(key, stmt); err != nil {
						return err
					}
				}
				mapper.mappers = mappers
			}
		case xml.EndElement:
			if token.Name.Local == "mappers" {
				return nil
			}
		}
	}
	return nil
}

func (p XMLParser) parseMapper(decoder *xml.Decoder, token xml.StartElement) (*Mapper, error) {
	mapper := &Mapper{}
	for _, attr := range token.Attr {
		mapper.setAttribute(attr.Name.Local, attr.Value)
	}

	if mapper.namespace = mapper.Attribute("namespace"); mapper.namespace == "" {
		return nil, errors.New("namespace is required")
	}

	if mapper.resource = mapper.Attribute("resource"); mapper.resource != "" {
		return p.parseMapperByResource(mapper.resource)
	}

	if mapper.url = mapper.Attribute("url"); mapper.url != "" {
		return p.parseMapperByURL(mapper.url)
	}

	mapper.statements = make(map[string]*Statement)

	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			action := Action(token.Name.Local)
			switch action {
			case Select, Insert, Update, Delete:
				stmt := &Statement{action: action, mapper: mapper}
				if err = p.parseStatement(stmt, decoder, token); err != nil {
					return nil, err
				}
				key := stmt.Key()
				if _, exists := mapper.statements[key]; exists {
					return nil, fmt.Errorf("duplicate statement id: %s", stmt.ID())
				}
				mapper.statements[key] = stmt
			case "sql":
				sqlNode := &SQLNode{mapper: mapper}
				if err = p.parseSQLNode(sqlNode, decoder, token); err != nil {
					return nil, err
				}
				if err = mapper.setSqlNode(sqlNode); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if token.Name.Local == "mapper" {
				return mapper, nil
			}
		}
	}
	return mapper, nil
}

func (p XMLParser) parseMapperByReader(reader io.Reader) (mapper *Mapper, err error) {
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "mapper" {
				if mapper, err = p.parseMapper(decoder, token); err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return mapper, err
}

func (p XMLParser) parseMapperByResource(resource string) (*Mapper, error) {
	var (
		reader io.ReadCloser
		err    error
	)
	reader, err = p.FS.Open(resource)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return p.parseMapperByReader(reader)
}

func (p XMLParser) parseMapperByHttpResponse(url string) (*Mapper, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return p.parseMapperByReader(resp.Body)
}

func (p XMLParser) parseMapperByURL(url string) (*Mapper, error) {
	// prepare url schema
	items := strings.Split(url, "://")
	if len(items) != 2 {
		return nil, fmt.Errorf("invalid url: %s", url)
	}
	schema := items[0]
	switch schema {
	case "file":
		return p.parseMapperByResource(items[1])
	case "http", "https":
		return p.parseMapperByHttpResponse(url)
	default:
		return nil, errors.New("invalid url schema")
	}
}

func (p XMLParser) parseStatement(stmt *Statement, decoder *xml.Decoder, token xml.StartElement) error {
	for _, attr := range token.Attr {
		stmt.setAttribute(attr.Name.Local, attr.Value)
	}
	if stmt.ID() == "" {
		return fmt.Errorf("%s statement id is required", stmt.Action())
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(stmt.Mapper(), decoder, token)
			if err != nil {
				return err
			}
			stmt.Nodes = append(stmt.Nodes, node)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				stmt.Nodes = append(stmt.Nodes, node)
			}
		case xml.EndElement:
			switch token.Name.Local {
			case stmt.action.String():
				return nil
			default:
				return fmt.Errorf("unexpected end element: %s", token.Name.Local)
			}
		}
	}
	return nil
}

func (p XMLParser) parseTags(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	switch token.Name.Local {
	case "if":
		return p.parseIf(mapper, decoder, token)
	case "where":
		return p.parseWhere(mapper, decoder)
	case "trim":
		return p.parseTrim(mapper, decoder, token)
	case "foreach":
		return p.parseForeach(mapper, decoder, token)
	case "set":
		return p.parseSet(mapper, decoder, token)
	case "include":
		return p.parseInclude(mapper, decoder, token)
	}
	return nil, fmt.Errorf("unknown tag: %s", token.Name.Local)
}

func (p XMLParser) parseInclude(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	var ref string
	for _, attr := range token.Attr {
		switch attr.Name.Local {
		case "refid":
			ref = attr.Value
		}
	}
	if ref == "" {
		return nil, errors.New("include ref is required")
	}

	includeNode := &IncludeNode{RefId: ref, mapper: mapper}

	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.EndElement:
			if token.Name.Local == "include" {
				return includeNode, nil
			}
		}
	}
	return includeNode, nil
}

func (p XMLParser) parseSet(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	setNode := &SetNode{}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(mapper, decoder, token)
			if err != nil {
				return nil, err
			}
			setNode.Nodes = append(setNode.Nodes, node)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				setNode.Nodes = append(setNode.Nodes, node)
			}
		case xml.EndElement:
			if token.Name.Local == "set" {
				return setNode, nil
			}
		}
	}
	return nil, nil
}

func (p XMLParser) parseIf(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	ifNode := &IfNode{}
	for _, attr := range token.Attr {
		if attr.Name.Local == "test" {
			ifNode.Test = attr.Value
			break
		}
	}
	if ifNode.Test == "" {
		return nil, errors.New("test is required")
	}

	// parse condition expression
	if err := ifNode.init(); err != nil {
		return nil, err
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(mapper, decoder, token)
			if err != nil {
				return nil, err
			}
			ifNode.Nodes = append(ifNode.Nodes, node)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				ifNode.Nodes = append(ifNode.Nodes, node)
			}
		case xml.EndElement:
			if token.Name.Local == "if" {
				return ifNode, nil
			}
		}
	}
	return ifNode, nil
}

func (p XMLParser) parseWhere(mapper *Mapper, decoder *xml.Decoder) (Node, error) {
	whereNode := &WhereNode{}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(mapper, decoder, token)
			if err != nil {
				return nil, err
			}
			whereNode.Nodes = append(whereNode.Nodes, node)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				whereNode.Nodes = append(whereNode.Nodes, node)
			}
		case xml.EndElement:
			if token.Name.Local == "where" {
				return whereNode, nil
			}
		}
	}
	return whereNode, nil
}

func (p XMLParser) parseTrim(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	trimNode := &TrimNode{}
	for _, attr := range token.Attr {
		if attr.Name.Local == "prefix" {
			trimNode.Prefix = attr.Value
		}
		if attr.Name.Local == "prefixOverrides" {
			trimNode.PrefixOverrides = attr.Value
		}
		if attr.Name.Local == "suffix" {
			trimNode.Suffix = attr.Value
		}
		if attr.Name.Local == "suffixOverrides" {
			trimNode.SuffixOverrides = attr.Value
		}
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(mapper, decoder, token)
			if err != nil {
				return nil, err
			}
			trimNode.Nodes = append(trimNode.Nodes, node)
		case xml.EndElement:
			if token.Name.Local == "trim" {
				return trimNode, nil
			}
		}
	}
	return trimNode, nil
}

func (p XMLParser) parseForeach(mapper *Mapper, decoder *xml.Decoder, token xml.StartElement) (Node, error) {
	foreachNode := &ForeachNode{}
	for _, attr := range token.Attr {
		if attr.Name.Local == "collection" {
			foreachNode.Collection = attr.Value
		}
		if attr.Name.Local == "item" {
			foreachNode.Item = attr.Value
		}
		if attr.Name.Local == "index" {
			foreachNode.Index = attr.Value
		}
		if attr.Name.Local == "open" {
			foreachNode.Open = attr.Value
		}
		if attr.Name.Local == "close" {
			foreachNode.Close = attr.Value
		}
		if attr.Name.Local == "separator" {
			foreachNode.Separator = attr.Value
		}
	}
	if foreachNode.Collection == "" {
		foreachNode.Collection = defaultParamKey
	}
	if foreachNode.Item == "" {
		return nil, errors.New("item is required")
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node, err := p.parseTags(mapper, decoder, token)
			if err != nil {
				return nil, err
			}
			foreachNode.Nodes = append(foreachNode.Nodes, node)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				foreachNode.Nodes = append(foreachNode.Nodes, node)
			}
		case xml.EndElement:
			if token.Name.Local == "foreach" {
				return foreachNode, nil
			}
		}
	}
	return foreachNode, nil
}

func (p XMLParser) parseCharData(decoder *xml.Decoder, endElementName string) (string, error) {
	var charData string
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		switch token := token.(type) {
		case xml.CharData:
			charData = string(token)
		case xml.EndElement:
			if token.Name.Local == endElementName {
				return charData, nil
			}
		}
	}
	return charData, nil
}

func (p XMLParser) parseIntCharData(decoder *xml.Decoder, endElementName string) (int, error) {
	charData, err := p.parseCharData(decoder, endElementName)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(charData)
}

func (p XMLParser) parseMaxIdleConnNum(decoder *xml.Decoder) (int, error) {
	return p.parseIntCharData(decoder, "maxIdleConnNum")
}

func (p XMLParser) parseDataSource(decoder *xml.Decoder) (string, error) {
	return p.parseCharData(decoder, "dataSource")
}

func (p XMLParser) parseDriver(decoder *xml.Decoder) (string, error) {
	return p.parseCharData(decoder, "driver")
}

func (p XMLParser) parseMaxOpenConnNum(decoder *xml.Decoder) (int, error) {
	return p.parseIntCharData(decoder, "maxOpenConnNum")
}

func (p XMLParser) parseMaxConnLifetime(decoder *xml.Decoder) (int, error) {
	return p.parseIntCharData(decoder, "maxConnLifetime")
}

func (p XMLParser) parseMaxIdleConnLifetime(decoder *xml.Decoder) (int, error) {
	return p.parseIntCharData(decoder, "maxIdleConnLifetime")
}

func (p XMLParser) parseSettings(decoder *xml.Decoder) (*Settings, error) {
	var setting Settings
	if err := decoder.DecodeElement(&setting, nil); err != nil {
		return nil, err
	}
	return &setting, nil
}

func (p XMLParser) parseSQLNode(sqlNode *SQLNode, decoder *xml.Decoder, token xml.StartElement) error {
	for _, attr := range token.Attr {
		if attr.Name.Local == "id" {
			sqlNode.id = attr.Value
			break
		}
	}
	if sqlNode.id == "" {
		return errors.New("id is required")
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			tags, err := p.parseTags(sqlNode.mapper, decoder, token)
			if err != nil {
				return err
			}
			sqlNode.nodes = append(sqlNode.nodes, tags)
		case xml.CharData:
			text := string(token)
			if char := strings.TrimSpace(text); char != "" {
				node := TextNode(char)
				sqlNode.nodes = append(sqlNode.nodes, node)
			}
		case xml.EndElement:
			if token.Name.Local == "sql" {
				return nil
			}
		}
	}
	return nil
}

func NewXMLConfigurationWithReader(reader io.Reader) (*Configuration, error) {
	parser := &XMLParser{FS: LocalFS{}}
	return parser.Parse(reader)
}

func NewXMLConfiguration(filename string) (*Configuration, error) {
	return NewXMLConfigurationWithFS(LocalFS{}, filename)
}

// NewXMLConfigurationWithFS creates a new Configuration from an XML file.
func NewXMLConfigurationWithFS(fs fs.FS, filename string) (*Configuration, error) {
	file, err := fs.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return NewXMLConfigurationWithReader(file)
}
