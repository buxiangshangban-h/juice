package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/eatmoreapple/juice"
)

type Generator struct {
	cfg       *juice.Configuration
	impl      *Implement
	namespace string
	args      string
	output    string
}

func (g *Generator) Generate() error {
	if !strings.HasSuffix(g.output, ".go") {
		g.output += ".go"
	}
	for i, method := range g.impl.Methods {
		key := fmt.Sprintf("%s.%s", g.namespace, method.Name)
		statement, err := g.cfg.Mappers.GetStatementByID(key)
		if err != nil {
			return err
		}
		maker := FunctionBodyMaker{statement: statement, function: &method}
		if err := maker.Make(); err != nil {
			return err
		}
		g.impl.Methods[i] = *maker.function
	}
	g.impl.ExtraImports = append(g.impl.ExtraImports, Import{Path: "github.com/eatmoreapple/juice", Name: "juice"})
	builder := strings.Builder{}
	builder.WriteString(fmt.Sprintf("// Code generated by \"%s\"; DO NOT EDIT.", g.args))
	builder.WriteString("\n\n")
	builder.WriteString(g.impl.String())
	return os.WriteFile(g.output, []byte(builder.String()), 0644)
}
